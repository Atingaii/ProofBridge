package hub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/automationassets"
)

// Explicit invocation is a separate opt-in mode. It must not be reported as
// automatic natural-language Skill selection.
func TestAutomationNativeSkillReadOnly(t *testing.T) {
	if os.Getenv("PROOFBRIDGE_TEST_REAL_SKILL") != "1" {
		t.Skip("set PROOFBRIDGE_TEST_REAL_SKILL=1 to allow one bounded model request per installed client")
	}
	if os.Getenv("PROOFBRIDGE_MANAGED_WORKER") == "1" {
		t.Skip("run the outer-client acceptance outside a managed ProofBridge worker")
	}
	mode := os.Getenv("PROOFBRIDGE_TEST_NATIVE_SKILL_MODE")
	if mode == "" {
		mode = "natural"
	}
	if mode != "natural" && mode != "explicit" {
		t.Fatal("PROOFBRIDGE_TEST_NATIVE_SKILL_MODE must be natural or explicit")
	}
	for _, client := range []string{"codex", "claude"} {
		t.Run(client+"_"+mode, func(t *testing.T) {
			path := nativeClientPath(t, client)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			s, st, uid, _ := newOrchestrationTestServer(t)
			trace := &nativeSkillHTTPTrace{}
			server := httptest.NewServer(trace.readOnlyHandler(s.httpSrv.Handler))
			defer server.Close()
			s.cfg.Automation.PublicURL = server.URL
			w := httptest.NewRecorder()
			s.handleAutomationAccess(w, httptest.NewRequest(http.MethodGet, "/api/automation/access", nil), uid)
			var access struct {
				Token        string `json:"token"`
				CodexConfig  string `json:"codexConfig"`
				ClaudeConfig string `json:"claudeConfig"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &access) != nil {
				t.Fatal("prepare isolated Skill access")
			}
			root := t.TempDir()
			home := filepath.Join(root, "home")
			project := filepath.Join(root, "project")
			for _, dir := range []string{home, project, filepath.Join(home, ".codex"), filepath.Join(home, ".claude")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			env := nativeSkillEnvironment(home, client)
			secrets := []string{access.Token}
			for _, entry := range os.Environ() {
				name, value, _ := strings.Cut(entry, "=")
				if len(value) > 8 && (strings.Contains(name, "TOKEN") || strings.Contains(name, "KEY")) {
					secrets = append(secrets, value)
				}
			}
			if client == "codex" {
				originalHome, err := os.UserHomeDir()
				if err != nil {
					t.Fatal(err)
				}
				originalCodex := os.Getenv("CODEX_HOME")
				if originalCodex == "" {
					originalCodex = filepath.Join(originalHome, ".codex")
				}
				if raw, err := os.ReadFile(filepath.Join(originalCodex, "auth.json")); err == nil {
					var auth struct {
						Mode   string                     `json:"auth_mode"`
						Tokens map[string]json.RawMessage `json:"tokens"`
					}
					if json.Unmarshal(raw, &auth) != nil || auth.Mode != "chatgpt" || len(auth.Tokens) == 0 {
						t.Skip("default OpenAI route requires confirmed ChatGPT OAuth; API-key/provider configuration is not copied or guessed")
					}
					if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), raw, 0o600); err != nil {
						t.Fatal(err)
					}
					var credentials any
					if json.Unmarshal(raw, &credentials) == nil {
						secrets = append(secrets, nativeSkillSecretStrings(credentials)...)
					}
				} else {
					t.Skip("Codex has no copyable confirmed ChatGPT OAuth; provider credentials are not rerouted")
				}
				// Root keys precede the MCP table so these are not nested server options.
				config := "approval_policy = \"on-request\"\nsandbox_mode = \"read-only\"\n" + access.CodexConfig
				if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				// Reuse existing process-scoped provider authentication, not user settings.
				// If file-based OAuth is used, copy only that credential file to the profile.
				originalHome, _ := os.UserHomeDir()
				originalClaude := os.Getenv("CLAUDE_CONFIG_DIR")
				if originalClaude == "" {
					originalClaude = filepath.Join(originalHome, ".claude")
				}
				provider, _ := url.Parse(os.Getenv("ANTHROPIC_BASE_URL"))
				copyOAuth := provider == nil || provider.Hostname() == "" || provider.Hostname() == "api.anthropic.com"
				if raw, err := os.ReadFile(filepath.Join(originalClaude, ".credentials.json")); err == nil && copyOAuth {
					if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), raw, 0o600); err != nil {
						t.Fatal(err)
					}
					var credentials any
					if json.Unmarshal(raw, &credentials) == nil {
						secrets = append(secrets, nativeSkillSecretStrings(credentials)...)
					}
				}
				if err := os.WriteFile(filepath.Join(home, ".claude", ".claude.json"), []byte(access.ClaudeConfig), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			installer := exec.CommandContext(ctx, "/bin/sh", "-s", "--", "--hub", server.URL, "--client", client, "--scope", "user")
			installer.Env, installer.Dir, installer.Stdin = env, project, strings.NewReader(automationassets.Installer)
			if _, err := installer.CombinedOutput(); err != nil {
				t.Fatalf("install temporary Skill: %v (output suppressed)", err)
			}
			prompt := "请使用 ProofBridge 查看我的账号上下文和已连接端点，不启动或改变任何任务，不保存设置，不执行项目命令。查询完成后用中文简短回显账号和端点状态。"
			if mode == "explicit" {
				if client == "codex" {
					prompt = "$proofbridge " + prompt
				} else {
					prompt = "/proofbridge " + prompt
				}
			}
			var cmd *exec.Cmd
			if client == "codex" {
				cmd = exec.CommandContext(ctx, path, "exec", "--json", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", prompt)
			} else {
				cmd = exec.CommandContext(ctx, path, "-p", prompt, "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--permission-mode", "plan", "--permission-prompts", "none", "--tools", "Read,Skill")
			}
			cmd.Env, cmd.Dir = env, project
			output, runErr := cmd.CombinedOutput()
			calls, successful, denied := trace.snapshot()
			t.Logf("mode=%s client=%s MCP calls=%v successful=%v denied=%v", mode, client, calls, successful, denied)
			if len(denied) != 0 {
				t.Fatal("client attempted a tool outside the read-only acceptance boundary; it was not executed")
			}
			runs, err := st.ListAllOrchestrationRuns(context.Background(), uid)
			if err != nil || len(runs) != 0 {
				t.Fatal("read-only acceptance unexpectedly changed run history")
			}
			if runErr != nil {
				t.Skipf("native %s inference unavailable: %s; exit=%v; no unbounded retry", client, nativeSkillFailure(output, ctx.Err()), runErr)
			}
			loaded, reply := nativeSkillOutput(client, output)
			t.Logf("Skill load trace observed=%v; terminal reply=%s", loaded, nativeSkillRedact(reply, secrets))
			if !successful["proofbridge_get_context"] || !successful["proofbridge_list_endpoints"] {
				t.Fatal("native client did not successfully call both required read-only tools")
			}
			if reply == "" {
				t.Fatal("native client did not return a terminal reply")
			}
			if !loaded {
				t.Error("MCP invocation passed, but no Skill load trace was observed; do not claim automatic Skill selection")
			}
		})
	}
}

type nativeSkillHTTPTrace struct {
	mu         sync.Mutex
	calls      []string
	successful map[string]bool
	denied     []string
}

func (tr *nativeSkillHTTPTrace) readOnlyHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := ""
		if r.URL.Path == "/mcp" && r.Method == http.MethodPost {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
			if err != nil {
				http.Error(w, "invalid test request", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var call struct {
				Method string `json:"method"`
				Params struct {
					Name string `json:"name"`
				} `json:"params"`
			}
			_ = json.Unmarshal(raw, &call)
			if call.Method == "tools/call" {
				name = call.Params.Name
				tr.mu.Lock()
				tr.calls = append(tr.calls, name)
				allowed := name == "proofbridge_get_context" || name == "proofbridge_list_endpoints"
				if !allowed {
					tr.denied = append(tr.denied, name)
				}
				tr.mu.Unlock()
				if !allowed {
					http.Error(w, "only account context and endpoint reads are allowed in this acceptance", http.StatusForbidden)
					return
				}
			}
		}
		recorder := &nativeSkillResponseTee{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if name != "" {
			var response struct {
				Result *struct {
					IsError bool `json:"isError"`
				} `json:"result"`
			}
			if json.Unmarshal(recorder.body.Bytes(), &response) == nil && response.Result != nil && !response.Result.IsError {
				tr.mu.Lock()
				if tr.successful == nil {
					tr.successful = map[string]bool{}
				}
				tr.successful[name] = true
				tr.mu.Unlock()
			}
		}
	})
}

type nativeSkillResponseTee struct {
	http.ResponseWriter
	body bytes.Buffer
}

func (w *nativeSkillResponseTee) Write(data []byte) (int, error) {
	_, _ = w.body.Write(data)
	return w.ResponseWriter.Write(data)
}

func (w *nativeSkillResponseTee) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (tr *nativeSkillHTTPTrace) snapshot() ([]string, map[string]bool, []string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	successful := map[string]bool{}
	for name, value := range tr.successful {
		successful[name] = value
	}
	return append([]string(nil), tr.calls...), successful, append([]string(nil), tr.denied...)
}

func nativeSkillEnvironment(home, client string) []string {
	env := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" || name == "CODEX_HOME" || name == "CLAUDE_CONFIG_DIR" || name == "CLAUDECODE" {
			continue
		}
		if client == "codex" && (strings.HasPrefix(name, "OPENAI_") || name == "CODEX_API_KEY" || strings.HasPrefix(name, "ANTHROPIC_") || name == "CLAUDE_CODE_OAUTH_TOKEN") {
			continue
		}
		if client == "claude" && (strings.HasPrefix(name, "OPENAI_") || name == "CODEX_API_KEY") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"), "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"), "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1")
}

func nativeSkillSecretStrings(value any) []string {
	var out []string
	switch v := value.(type) {
	case string:
		if len(v) > 8 {
			out = append(out, v)
		}
	case map[string]any:
		for _, item := range v {
			out = append(out, nativeSkillSecretStrings(item)...)
		}
	case []any:
		for _, item := range v {
			out = append(out, nativeSkillSecretStrings(item)...)
		}
	}
	return out
}

func nativeSkillRedact(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return text
}

func nativeSkillFailure(output []byte, contextErr error) string {
	if contextErr != nil {
		return "60s deadline exceeded"
	}
	lower := strings.ToLower(string(output))
	for _, item := range []struct{ fragment, label string }{
		{"approval_policy = \"untrusted\" is no longer supported", "installed CLI rejects the legacy untrusted approval setting before inference"},
		{"unauthorized", "provider authentication rejected"}, {"invalid api key", "provider authentication rejected"},
		{"not logged in", "client login required"}, {"401", "provider returned 401"},
		{"403", "provider returned 403"}, {"usage limit", "provider usage limit"},
		{"rate limit", "provider rate limit"}, {"not found", "provider/model/config not found"},
		{"error sending request", "provider connection failed"}, {"permission", "client permission restriction"},
	} {
		if strings.Contains(lower, item.fragment) {
			return item.label
		}
	}
	return "client or provider error (raw output suppressed)"
}

func nativeSkillOutput(client string, output []byte) (bool, string) {
	loaded := false
	var replies []string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if client == "codex" {
			item, _ := event["item"].(map[string]any)
			kind, _ := item["type"].(string)
			command, _ := item["command"].(string)
			if event["type"] == "item.completed" && kind == "command_execution" && strings.Contains(command, "proofbridge/SKILL.md") && item["exit_code"] == float64(0) {
				loaded = true
			}
			if event["type"] == "item.completed" && kind == "agent_message" {
				if text, _ := item["text"].(string); text != "" {
					replies = append(replies, text)
				}
			}
		} else {
			message, _ := event["message"].(map[string]any)
			content, _ := message["content"].([]any)
			for _, raw := range content {
				block, _ := raw.(map[string]any)
				if block["type"] == "tool_use" && block["name"] == "Skill" {
					input, _ := block["input"].(map[string]any)
					loaded = loaded || input["skill"] == "proofbridge"
				}
			}
			if event["type"] == "result" {
				if text, _ := event["result"].(string); text != "" {
					replies = append(replies, text)
				}
			}
		}
	}
	return loaded, strings.Join(replies, "\n")
}
