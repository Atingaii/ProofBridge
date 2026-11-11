package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutomationNativeSkillDiagnosticEvidenceAndStrictGate(t *testing.T) {
	codexLoad := `{"type":"item.completed","item":{"type":"command_execution","command":"cat /isolated/skills/proofbridge/SKILL.md","exit_code":0}}` + "\n"
	codexReply := `{"type":"item.completed","item":{"type":"agent_message","text":"private@example.test fixture-personal-token"}}` + "\n"
	claudeLoad := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"proofbridge"}}]}}` + "\n"
	claudeReply := `{"type":"result","is_error":false,"result":"private@example.test fixture-personal-token"}` + "\n"
	for _, tc := range []struct {
		name, client, output string
		loaded, reply        bool
	}{
		{"codex-observed", "codex", codexLoad + codexReply, true, true},
		{"claude-observed", "claude", claudeLoad + claudeReply, true, true},
		{"codex-self-report-is-not-loading", "codex", codexReply, false, true},
		{"claude-explicit-marker-is-not-loading", "claude", `{"type":"result","result":"/proofbridge: I loaded the Skill"}`, false, true},
		{"failed-read-is-not-loading", "codex", strings.Replace(codexLoad, `"exit_code":0`, `"exit_code":1`, 1), false, false},
		{"other-skill-is-not-loading", "codex", strings.Replace(codexLoad, "proofbridge/SKILL.md", "other/SKILL.md", 1), false, false},
		{"no-terminal-reply", "claude", claudeLoad + `{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`, true, false},
		{"failed-terminal-result-is-not-reply", "claude", claudeLoad + `{"type":"result","is_error":true,"result":"provider rejected"}`, true, false},
		{"partial-last-event-retains-earlier-evidence", "codex", codexLoad + `{"type":`, true, false},
		{"whitespace-is-not-reply", "codex", `{"type":"item.completed","item":{"type":"agent_message","text":"  \n"}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := nativeSkillOutput(tc.client, []byte(tc.output))
			if e.Loaded != tc.loaded || e.ReplyObserved != tc.reply {
				t.Fatal("stream evidence did not match the observable loading and terminal events")
			}
			raw, _ := json.Marshal(e)
			if strings.Contains(string(raw), "private@example.test") || strings.Contains(string(raw), "fixture-personal-token") {
				t.Fatal("diagnostic evidence retained terminal content")
			}
		})
	}
	full := nativeSkillOutput("codex", []byte(codexLoad+codexReply))
	successful := map[string]bool{"proofbridge_get_context": true, "proofbridge_list_endpoints": true}
	for _, tc := range []struct {
		name    string
		e       nativeSkillEvidence
		calls   map[string]bool
		runErr  error
		ctxErr  error
		outcome string
	}{
		{"all-observed", full, successful, nil, nil, "pass"},
		{"loading-missing", nativeSkillEvidence{ReplyObserved: true}, successful, nil, nil, "fail"},
		{"terminal-missing", nativeSkillEvidence{Loaded: true}, successful, nil, nil, "fail"},
		{"tool-missing", full, map[string]bool{"proofbridge_get_context": true}, nil, nil, "fail"},
		{"deadline-with-complete-evidence-still-skips", full, successful, errors.New("killed"), context.DeadlineExceeded, "skip"},
		{"deadline-with-zero-exit-still-skips", full, successful, nil, context.DeadlineExceeded, "skip"},
		{"nonzero-exit-still-skips", full, successful, errors.New("provider unavailable"), nil, "skip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeSkillProbeStatus(tc.e, tc.calls, tc.runErr, tc.ctxErr); got != tc.outcome {
				t.Fatalf("probe outcome=%s, want %s", got, tc.outcome)
			}
		})
	}
}

func TestAutomationNativeSkillDiagnosticFailureEvidencePrecedesDeadline(t *testing.T) {
	for _, tc := range []struct{ output, want string }{
		{"Unauthorized fixture-personal-token", "provider authentication rejected"},
		{"invalid_api_key", "provider authentication rejected"},
		{"rate_limit_exceeded", "provider rate limit"},
		{"usage_limit_reached", "provider usage limit"},
		{"error sending request", "provider connection failed"},
		{"HTTP 403", "provider returned 403"},
		{`{"type":"error","status":401,"message":"request rejected"}`, "provider returned 401"},
		{`{"type":"turn.failed","error":{"status":403,"message":"request rejected"}}`, "provider returned 403"},
		{`{"type":"result","is_error":true,"result":"rate_limit_exceeded"}`, "provider rate limit"},
		{`{"type":"thread.started","id":"fixture-401-403"}`, "120s inference deadline exceeded"},
		{`{"type":"turn.completed","usage":{"input_tokens":401,"cached_input_tokens":403}}`, "120s inference deadline exceeded"},
		{`{"type":"item.completed","item":{"type":"agent_message","text":"HTTP 401: not found; permission denied"}}`, "120s inference deadline exceeded"},
		{`{"type":"result","is_error":false,"result":"not found; permission denied"}`, "120s inference deadline exceeded"},
		{"", "120s inference deadline exceeded"},
	} {
		if got := nativeSkillFailure([]byte(tc.output), context.DeadlineExceeded); got != tc.want {
			t.Fatalf("safe failure category=%q, want %q", got, tc.want)
		}
	}
	if got := nativeSkillFailure(nil, context.Canceled); got != "inference canceled" {
		t.Fatal("cancellation was labeled as a deadline")
	}
	if nativeSkillPrepareTimeout != 30*time.Second || nativeSkillInferenceTimeout != 120*time.Second {
		t.Fatal("installation and inference must retain separate bounded windows")
	}
}

func TestAutomationNativeSkillDiagnosticRPCNamesAndLongListener(t *testing.T) {
	trace := &nativeSkillHTTPTrace{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseListener := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseListener()
	backendCalls := 0
	handler := trace.readOnlyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct{ Method string }
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if msg.Method == "subscriptions/listen" {
			close(entered)
			<-release
		} else {
			backendCalls++
		}
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"isError":false,"account":"private@example.test","token":"fixture-personal-token"}}`)
	}))
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"subscriptions/listen"}`)))
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("listener fixture did not start")
	}
	_, _, _, methods := trace.snapshot()
	if len(methods) != 1 || methods[0].Method != "subscriptions/listen" || methods[0].Completed {
		t.Fatal("pending listener method must be observable before its handler completes")
	}
	for _, name := range []string{"proofbridge_get_context", "proofbridge_list_endpoints", "proofbridge_start_run", "private@example.test"} {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]string{"name": name}})
		r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer fixture-personal-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if name != "proofbridge_get_context" && name != "proofbridge_list_endpoints" && w.Code != http.StatusForbidden {
			t.Fatal("read-only fixture allowed an out-of-bound tool")
		}
	}
	releaseListener()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("released listener fixture did not finish")
	}
	calls, successful, denied, methods := trace.snapshot()
	if backendCalls != 2 || !successful["proofbridge_get_context"] || !successful["proofbridge_list_endpoints"] || len(denied) != 2 || len(calls) != 4 {
		t.Fatal("listener tracing changed the two-tool acceptance boundary")
	}
	if !methods[0].Completed || len(methods) != 5 {
		t.Fatal("completed transport requests are missing from diagnostic snapshot")
	}
	raw, _ := json.Marshal([]any{calls, successful, denied, methods})
	if strings.Contains(string(raw), "private@example.test") || strings.Contains(string(raw), "fixture-personal-token") || strings.Contains(string(raw), "Authorization") {
		t.Fatal("RPC diagnostics retained headers, response content or an invalid identifier")
	}
}

func TestAutomationNativeSkillDiagnosticOfficialRouteIsExplicit(t *testing.T) {
	config := nativeSkillCodexConfig("[mcp_servers.proofbridge]\nurl = \"http://fixture.invalid/mcp\"\n")
	root := strings.Split(config, "[mcp_servers.")[0]
	for _, setting := range []string{`model_provider = "openai"`, `model = "gpt-6.1-sol"`, `model_reasoning_effort = "low"`, `sandbox_mode = "read-only"`, `approval_policy = "on-request"`} {
		if !strings.Contains(root, setting) {
			t.Fatal("isolated official route parameters must remain root-level explicit settings")
		}
	}
	t.Setenv("OPENAI_API_KEY", "fixture-custom-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "fixture-claude-auth")
	t.Setenv("ANTHROPIC_DEFAULT_SONNET_MODEL", "fixture-sonnet")
	for _, entry := range nativeSkillEnvironment(t.TempDir(), "codex") {
		if strings.HasPrefix(entry, "OPENAI_") || strings.HasPrefix(entry, "ANTHROPIC_") {
			t.Fatal("official Codex fixture inherited custom provider credentials")
		}
	}
	claude := strings.Join(nativeSkillEnvironment(t.TempDir(), "claude"), "\n")
	if !strings.Contains(claude, "ANTHROPIC_AUTH_TOKEN=fixture-claude-auth") || !strings.Contains(claude, "ANTHROPIC_DEFAULT_SONNET_MODEL=fixture-sonnet") {
		t.Fatal("isolated Claude fixture dropped process authentication or aliases")
	}
}
