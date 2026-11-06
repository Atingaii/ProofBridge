package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// This opt-in gate uses installed clients, but never starts an inference turn or
// reads/copies the operator's CLI credentials. All configuration is temporary.
func TestAutomationNativeMCPClients(t *testing.T) {
	if os.Getenv("PROOFBRIDGE_TEST_REAL_CLIENTS") != "1" {
		t.Skip("set PROOFBRIDGE_TEST_REAL_CLIENTS=1 to test installed Codex and Claude Code")
	}
	s, _, uid, _ := newOrchestrationTestServer(t)
	server := httptest.NewServer(s.httpSrv.Handler)
	defer server.Close()
	s.cfg.Automation.PublicURL = server.URL
	w := httptest.NewRecorder()
	s.handleAutomationAccess(w, httptest.NewRequest(http.MethodGet, "/api/automation/access", nil), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("prepare native-client access: HTTP %d", w.Code)
	}
	var access struct {
		CodexConfig  string `json:"codexConfig"`
		ClaudeConfig string `json:"claudeConfig"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &access); err != nil {
		t.Fatal("decode native-client configuration")
	}
	t.Run("CodexAppServer", func(t *testing.T) {
		path := nativeClientPath(t, "codex")
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		root := t.TempDir()
		env, project := nativeClientEnvironment(t, root)
		if err := os.WriteFile(filepath.Join(root, "codex", "config.toml"), []byte(access.CodexConfig), 0o600); err != nil {
			t.Fatal(err)
		}
		logNativeClientVersion(t, ctx, path, env, project)
		cmd := exec.CommandContext(ctx, path, "app-server", "--listen", "stdio://")
		cmd.Env, cmd.Dir, cmd.Stderr = env, project, io.Discard
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start native Codex: %v", err)
		}
		defer func() { cancel(); _ = in.Close(); _ = cmd.Wait() }()
		type message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		messages := make(chan message, 64)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			scanner := bufio.NewScanner(out)
			scanner.Buffer(make([]byte, 4096), 8<<20)
			for scanner.Scan() {
				var m message
				if json.Unmarshal(scanner.Bytes(), &m) != nil || len(m.ID) == 0 {
					continue
				}
				select {
				case messages <- m:
				case <-ctx.Done():
					return
				}
			}
		}()
		encoder := json.NewEncoder(in)
		nextID := 0
		request := func(method string, params any) json.RawMessage {
			t.Helper()
			nextID++
			if err := encoder.Encode(map[string]any{"id": nextID, "method": method, "params": params}); err != nil {
				t.Fatalf("write Codex %s: %v", method, err)
			}
			for {
				select {
				case m := <-messages:
					if string(m.ID) != fmt.Sprint(nextID) {
						continue
					}
					if len(m.Error) != 0 && string(m.Error) != "null" {
						t.Fatalf("native Codex rejected %s (response content suppressed)", method)
					}
					return m.Result
				case <-readDone:
					t.Fatalf("native Codex output closed during %s", method)
				case <-ctx.Done():
					t.Fatalf("native Codex %s exceeded the 60s limit", method)
				}
			}
		}
		request("initialize", map[string]any{
			"clientInfo":   map[string]string{"name": "proofbridge-native-test", "title": "ProofBridge native MCP test", "version": "1"},
			"capabilities": map[string]any{"experimentalApi": true},
		})
		if err := encoder.Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
			t.Fatal(err)
		}
		raw := request("mcpServerStatus/list", map[string]any{"limit": 100, "serverName": "proofbridge", "detail": "toolsAndAuthOnly"})
		var status struct {
			Data []struct {
				Name  string                     `json:"name"`
				Tools map[string]json.RawMessage `json:"tools"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &status) != nil {
			t.Fatal("decode Codex MCP status")
		}
		for _, item := range status.Data {
			if item.Name != "proofbridge" {
				continue
			}
			names := make([]string, 0, len(item.Tools))
			for name := range item.Tools {
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) != 20 || !strings.Contains(strings.Join(names, ","), "proofbridge_get_context") {
				t.Fatalf("native Codex discovered %d tools, expected the 20 public tools", len(names))
			}
			t.Logf("native Codex initialized ProofBridge and discovered all %d tools", len(names))
			return
		}
		t.Fatal("native Codex did not report the configured proofbridge server")
	})
	t.Run("ClaudeMCPList", func(t *testing.T) {
		path := nativeClientPath(t, "claude")
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		root := t.TempDir()
		env, project := nativeClientEnvironment(t, root)
		logNativeClientVersion(t, ctx, path, env, project)
		// Ask the CLI to select its user config path without putting a secret in
		// argv. Then merge the exact Hub-generated mcpServers object on disk.
		placeholder, _ := json.Marshal(map[string]string{"type": "http", "url": server.URL + "/mcp"})
		add := exec.CommandContext(ctx, path, "mcp", "add-json", "--scope", "user", "proofbridge", string(placeholder))
		add.Env, add.Dir = env, project
		if _, err := add.CombinedOutput(); err != nil {
			t.Fatalf("prepare isolated Claude MCP settings: %v (output suppressed)", err)
		}
		var generated map[string]json.RawMessage
		if json.Unmarshal([]byte(access.ClaudeConfig), &generated) != nil {
			t.Fatal("decode generated Claude MCP template")
		}
		configured := false
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Name() != ".claude.json" {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var settings map[string]json.RawMessage
			if json.Unmarshal(raw, &settings) != nil || len(settings["mcpServers"]) == 0 {
				return nil
			}
			settings["mcpServers"] = generated["mcpServers"]
			raw, err = json.Marshal(settings)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				return err
			}
			configured = true
			return nil
		})
		if err != nil || !configured {
			t.Fatal("could not locate the isolated Claude user MCP settings")
		}
		list := exec.CommandContext(ctx, path, "mcp", "list")
		list.Env, list.Dir = env, project
		output, err := list.CombinedOutput()
		if err != nil {
			t.Fatalf("native Claude MCP list: %v (output suppressed)", err)
		}
		if !strings.Contains(string(output), "proofbridge") || !strings.Contains(string(output), "Connected") {
			t.Fatal("native Claude did not report ProofBridge Connected (output suppressed)")
		}
		t.Log("native Claude MCP list reports ProofBridge Connected")
	})
}

func nativeClientPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("native %s is not installed", name)
	}
	return path
}

func nativeClientEnvironment(t *testing.T, root string) ([]string, string) {
	t.Helper()
	for _, name := range []string{"home", "codex", "claude", "project"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	blocked := map[string]bool{
		"HOME": true, "CODEX_HOME": true, "CLAUDE_CONFIG_DIR": true,
		"OPENAI_API_KEY": true, "CODEX_API_KEY": true, "ANTHROPIC_API_KEY": true,
		"ANTHROPIC_AUTH_TOKEN": true, "CLAUDE_CODE_OAUTH_TOKEN": true, "CLAUDECODE": true,
	}
	env := []string{}
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if !blocked[name] {
			env = append(env, value)
		}
	}
	env = append(env, "HOME="+filepath.Join(root, "home"), "CODEX_HOME="+filepath.Join(root, "codex"), "CLAUDE_CONFIG_DIR="+filepath.Join(root, "claude"), "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1")
	return env, filepath.Join(root, "project")
}

func logNativeClientVersion(t *testing.T, ctx context.Context, path string, env []string, dir string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env, cmd.Dir = env, dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native client version: %v", err)
	}
	t.Logf("native client version: %s", strings.TrimSpace(string(output)))
}
