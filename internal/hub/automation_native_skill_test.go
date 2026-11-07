package hub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/automationassets"
)

const (
	nativeSkillPrepareTimeout   = 30 * time.Second
	nativeSkillInferenceTimeout = 120 * time.Second
	nativeSkillCodexModel       = "gpt-6.1-sol"
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
	alias := os.Getenv("PROOFBRIDGE_TEST_SKILL_ALIAS")
	if alias == "" {
		alias = "ProofBridge"
	}
	if alias != "ProofBridge" && alias != "Codex Bridge" && alias != "CodexBridge" {
		t.Fatal("PROOFBRIDGE_TEST_SKILL_ALIAS must name ProofBridge, Codex Bridge or CodexBridge")
	}
	for _, client := range []string{"codex", "claude"} {
		t.Run(client+"_"+mode, func(t *testing.T) {
			path := nativeClientPath(t, client)
			prepareStarted := time.Now()
			prepareCtx, prepareCancel := context.WithTimeout(context.Background(), nativeSkillPrepareTimeout)
			defer prepareCancel()
			s, st, uid, _ := newOrchestrationTestServer(t)
			trace := &nativeSkillHTTPTrace{}
			server := httptest.NewServer(trace.readOnlyHandler(s.httpSrv.Handler))
			defer server.Close()
			defer server.CloseClientConnections()
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
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal("resolve temporary native profile")
			}
			home := filepath.Join(root, "home")
			project := filepath.Join(root, "project")
			for _, dir := range []string{home, project, filepath.Join(home, ".codex"), filepath.Join(home, ".claude")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			env := nativeSkillEnvironment(home, client)
			if client == "codex" {
				t.Logf("client=codex route=official-isolated provider=openai model=%s effort=low; user custom configuration is not copied", nativeSkillCodexModel)
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
				} else {
					t.Skip("Codex has no copyable confirmed ChatGPT OAuth; provider credentials are not rerouted")
				}
				// Root keys precede the MCP table so these are not nested server options.
				config := nativeSkillCodexConfig(access.CodexConfig)
				if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Log("client=claude route=process-auth-isolated; process provider authentication and aliases retained, user settings not copied")
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
				}
				if err := os.WriteFile(filepath.Join(home, ".claude", ".claude.json"), []byte(access.ClaudeConfig), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			installer := exec.CommandContext(prepareCtx, "/bin/sh", "-s", "--", "--asset-base", server.URL+"/skills/proofbridge", "--client", client, "--scope", "user")
			installer.WaitDelay = 2 * time.Second
			installer.Env, installer.Dir, installer.Stdin = env, project, strings.NewReader(automationassets.Installer)
			if _, err := installer.CombinedOutput(); err != nil {
				t.Fatalf("phase=preparation elapsed_ms=%d deadline=%v; temporary Skill installation failed (output suppressed)", time.Since(prepareStarted).Milliseconds(), errors.Is(prepareCtx.Err(), context.DeadlineExceeded))
			}
			var expansion *nativeSkillExpansionFixture
			var expansionSettings string
			if client == "claude" && mode == "explicit" {
				var err error
				expansion, err = newNativeSkillExpansionFixture(home, project, root)
				if err != nil {
					t.Fatal("temporary explicit Skill namespace could not be verified")
				}
				var hookEnv string
				expansionSettings, hookEnv, err = expansion.hookSettings(root)
				if err != nil {
					t.Fatal("temporary expansion observer could not be prepared")
				}
				env = append(env, hookEnv)
			}
			t.Logf("phase=preparation elapsed_ms=%d", time.Since(prepareStarted).Milliseconds())
			prepareCancel()
			prompt := "请使用 " + alias + " 查看我的账号上下文和已连接端点，不启动或改变任何任务，不保存设置，不执行项目命令。查询完成后用中文简短回显账号和端点状态。"
			if mode == "explicit" {
				if client == "codex" {
					prompt = "$proofbridge " + prompt
				} else {
					prompt = "/proofbridge " + prompt
				}
			}
			// Installation time does not consume this independent inference window.
			ctx, cancel := context.WithTimeout(context.Background(), nativeSkillInferenceTimeout)
			defer cancel()
			inferenceStarted := time.Now()
			var cmd *exec.Cmd
			if client == "codex" {
				cmd = exec.CommandContext(ctx, path, "exec", "--json", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", prompt)
			} else {
				args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--permission-mode", "plan", "--permission-prompts", "none", "--tools", "Read,Skill"}
				if expansionSettings != "" {
					args = append(args, "--settings", expansionSettings, "--include-hook-events")
				}
				cmd = exec.CommandContext(ctx, path, args...)
			}
			cmd.WaitDelay = 2 * time.Second
			cmd.Env, cmd.Dir = env, project
			output, runErr := cmd.CombinedOutput()
			evidence := nativeSkillOutput(client, output)
			if expansion != nil {
				observed := expansion.observe(output)
				evidence = nativeSkillCorrelateExpansion(evidence, observed)
				t.Logf("explicit_expansion_receipt_valid=%v client_rendered_skill_body_observed=%v", observed.ReceiptValid, observed.BodyMatched)
			}
			calls, successful, denied, methods := trace.snapshot()
			t.Logf("mode=%s client=%s alias=%s MCP calls=%v successful=%v denied=%v", mode, client, alias, calls, successful, denied)
			t.Logf("phase=inference elapsed_ms=%d limit_ms=%d RPC=%v event_types=%v Skill_load_observed=%v terminal_reply_observed=%v", time.Since(inferenceStarted).Milliseconds(), nativeSkillInferenceTimeout.Milliseconds(), methods, evidence.EventTypes, evidence.Loaded, evidence.ReplyObserved)
			if len(denied) != 0 {
				t.Fatal("client attempted a tool outside the read-only acceptance boundary; it was not executed")
			}
			runs, err := st.ListAllOrchestrationRuns(context.Background(), uid)
			if err != nil || len(runs) != 0 {
				t.Fatal("read-only acceptance unexpectedly changed run history")
			}
			if nativeSkillProbeStatus(evidence, successful, runErr, ctx.Err()) == "skip" {
				t.Skipf("native %s inference unavailable: %s; exit=%v; no unbounded retry", client, nativeSkillFailure(output, ctx.Err()), runErr)
			}
			if !successful["proofbridge_get_context"] || !successful["proofbridge_list_endpoints"] {
				t.Fatal("native client did not successfully call both required read-only tools")
			}
			if !evidence.ReplyObserved {
				t.Fatal("native client did not return a terminal reply")
			}
			if !evidence.Loaded {
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
	methods    []nativeSkillRPCObservation
	started    []time.Time
}

// This contains names and timings only. In particular, params, headers, tool
// results and account fields never enter the diagnostic snapshot.
type nativeSkillRPCObservation struct {
	Method    string
	ElapsedMS int64
	Completed bool
}

func (o nativeSkillRPCObservation) String() string {
	return fmt.Sprintf("%s elapsed_ms=%d completed=%t", o.Method, o.ElapsedMS, o.Completed)
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
			started := time.Now()
			tr.mu.Lock()
			index := len(tr.methods)
			tr.methods = append(tr.methods, nativeSkillRPCObservation{Method: nativeSkillTraceName(call.Method)})
			tr.started = append(tr.started, started)
			tr.mu.Unlock()
			defer func() {
				tr.mu.Lock()
				tr.methods[index].ElapsedMS = time.Since(started).Milliseconds()
				tr.methods[index].Completed = true
				tr.mu.Unlock()
			}()
			if call.Method == "tools/call" {
				name = call.Params.Name
				tr.mu.Lock()
				tr.calls = append(tr.calls, nativeSkillTraceName(name))
				allowed := name == "proofbridge_get_context" || name == "proofbridge_list_endpoints"
				if !allowed {
					tr.denied = append(tr.denied, nativeSkillTraceName(name))
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

func (tr *nativeSkillHTTPTrace) snapshot() ([]string, map[string]bool, []string, []nativeSkillRPCObservation) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	successful := map[string]bool{}
	for name, value := range tr.successful {
		successful[name] = value
	}
	methods := append([]nativeSkillRPCObservation(nil), tr.methods...)
	for i := range methods {
		if !methods[i].Completed {
			methods[i].ElapsedMS = time.Since(tr.started[i]).Milliseconds()
		}
	}
	return append([]string(nil), tr.calls...), successful, append([]string(nil), tr.denied...), methods
}

func nativeSkillCodexConfig(mcpConfig string) string {
	return "model_provider = \"openai\"\nmodel = \"" + nativeSkillCodexModel + "\"\nmodel_reasoning_effort = \"low\"\napproval_policy = \"on-request\"\nsandbox_mode = \"read-only\"\n" + mcpConfig
}

func nativeSkillTraceName(name string) string {
	if len(name) == 0 || len(name) > 128 {
		return "[invalid-name]"
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_./:-", c)) {
			return "[invalid-name]"
		}
	}
	return name
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

func nativeSkillFailure(output []byte, contextErr error) string {
	lower := strings.ToLower(nativeSkillErrorText(output))
	for _, item := range []struct{ fragment, label string }{
		{"approval_policy = \"untrusted\" is no longer supported", "installed CLI rejects the legacy untrusted approval setting before inference"},
		{"unauthorized", "provider authentication rejected"}, {"invalid api key", "provider authentication rejected"},
		{"invalid_api_key", "provider authentication rejected"}, {"authentication_error", "provider authentication rejected"},
		{"not logged in", "client login required"},
		{"status 401", "provider returned 401"}, {"http 401", "provider returned 401"},
		{"\"status\":401", "provider returned 401"}, {"\"status\": 401", "provider returned 401"},
		{"status 403", "provider returned 403"}, {"http 403", "provider returned 403"},
		{"403 forbidden", "provider returned 403"}, {"\"status\":403", "provider returned 403"},
		{"\"status\": 403", "provider returned 403"}, {"usage limit", "provider usage limit"},
		{"usage_limit", "provider usage limit"}, {"insufficient_quota", "provider usage limit"},
		{"rate limit", "provider rate limit"}, {"rate_limit", "provider rate limit"}, {"too many requests", "provider rate limit"},
		{"not found", "provider/model/config not found"},
		{"error sending request", "provider connection failed"}, {"permission denied", "client permission restriction"},
	} {
		if strings.Contains(lower, item.fragment) {
			return item.label
		}
	}
	if errors.Is(contextErr, context.DeadlineExceeded) {
		return "120s inference deadline exceeded"
	}
	if contextErr != nil {
		return "inference canceled"
	}
	return "client or provider error (raw output suppressed)"
}

// Streamed tool results, usage and agent prose are not provider error evidence.
// Non-JSON lines are CLI stderr/startup diagnostics. Structured lines must
// identify an error before their bounded classification fields are inspected.
// The returned text is used in memory only and must never be logged.
func nativeSkillErrorText(output []byte) string {
	var messages []string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			messages = append(messages, string(line))
			continue
		}
		kind, _ := event["type"].(string)
		subtype, _ := event["subtype"].(string)
		if kind != "error" && kind != "turn.failed" && event["is_error"] != true && event["error"] == nil && !(kind == "result" && strings.HasPrefix(subtype, "error")) {
			continue
		}
		for _, field := range []string{"message", "error", "result", "code"} {
			if value, ok := event[field]; ok {
				if text, ok := value.(string); ok {
					messages = append(messages, text)
				} else if raw, err := json.Marshal(value); err == nil {
					messages = append(messages, string(raw))
				}
			}
		}
		for _, field := range []string{"status", "status_code"} {
			if value, ok := event[field]; ok {
				messages = append(messages, fmt.Sprintf("HTTP %v", value))
			}
		}
	}
	return strings.Join(messages, "\n")
}

type nativeSkillEvidence struct {
	Loaded        bool
	ReplyObserved bool
	EventTypes    []string
}

func nativeSkillProbeStatus(e nativeSkillEvidence, successful map[string]bool, runErr, contextErr error) string {
	if runErr != nil || contextErr != nil {
		return "skip"
	}
	if !e.Loaded || !e.ReplyObserved || !successful["proofbridge_get_context"] || !successful["proofbridge_list_endpoints"] {
		return "fail"
	}
	return "pass"
}

func nativeSkillOutput(client string, output []byte) nativeSkillEvidence {
	var evidence nativeSkillEvidence
	types := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if kind, ok := event["type"].(string); ok {
			name := nativeSkillTraceName(kind)
			if subtype, _ := event["subtype"].(string); subtype != "" {
				name += "/" + nativeSkillTraceName(subtype)
			}
			types[name] = true
		}
		if client == "codex" {
			item, _ := event["item"].(map[string]any)
			kind, _ := item["type"].(string)
			command, _ := item["command"].(string)
			if event["type"] == "item.completed" && kind == "command_execution" && strings.Contains(command, "proofbridge/SKILL.md") && item["exit_code"] == float64(0) {
				evidence.Loaded = true
			}
			if event["type"] == "item.completed" && kind == "agent_message" {
				text, _ := item["text"].(string)
				evidence.ReplyObserved = evidence.ReplyObserved || strings.TrimSpace(text) != ""
			}
		} else {
			message, _ := event["message"].(map[string]any)
			content, _ := message["content"].([]any)
			for _, raw := range content {
				block, _ := raw.(map[string]any)
				if block["type"] == "tool_use" && block["name"] == "Skill" {
					input, _ := block["input"].(map[string]any)
					evidence.Loaded = evidence.Loaded || input["skill"] == "proofbridge"
				}
			}
			if event["type"] == "result" && event["is_error"] != true {
				text, _ := event["result"].(string)
				evidence.ReplyObserved = evidence.ReplyObserved || strings.TrimSpace(text) != ""
			}
		}
	}
	for kind := range types {
		evidence.EventTypes = append(evidence.EventTypes, kind)
	}
	sort.Strings(evidence.EventTypes)
	return evidence
}
