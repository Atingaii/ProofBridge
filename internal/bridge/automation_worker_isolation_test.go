package bridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
)

func writeAutomationFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestManagedWorkerCodexPreservesProfilesAndPreciselyDisablesOuterEntrypoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, "private-profile")
	project := filepath.Join(home, "project")
	cwd := filepath.Join(project, "nested")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(profile, "config.toml")
	original := `model="worker-model"
[mcp_servers.proofbridge]
url="https://proofbridge.invalid/mcp"
[mcp_servers.codex_bridge]
command="approval-helper"
[mcp_servers.unrelated]
command="other"
[[skills.config]]
path="/existing/disabled/SKILL.md"
enabled=false
[[skills.config]]
name="unrelated-skill"
enabled=false
`
	writeAutomationFixture(t, configFile, original)
	paths := []string{filepath.Join(home, ".agents", "skills", "proofbridge", "SKILL.md"), filepath.Join(profile, "skills", "proofbridge", "SKILL.md"), filepath.Join(project, ".agents", "skills", "proofbridge", "SKILL.md")}
	for _, path := range paths {
		writeAutomationFixture(t, path, "---\nname: proofbridge\ndescription: proofbridge\n---\nTest\n")
	}
	cmd := exec.Command("codex", "exec", "resume", "--json", "native-thread", "continue")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+profile, "PROOFBRIDGE_MANAGED_WORKER=0")
	before := append([]string(nil), cmd.Args...)
	if err := isolateAutomationEntrypoint(cmd, "codex", cwd); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cmd.Args[len(cmd.Args)-len(before)+3:], before[3:]) {
		t.Fatalf("resume arguments changed: %#v", cmd.Args)
	}
	if commandEnvValues(cmd, "PROOFBRIDGE_MANAGED_WORKER")["PROOFBRIDGE_MANAGED_WORKER"] != "1" {
		t.Fatal("worker marker missing")
	}
	if commandEnvValues(cmd, "CODEX_HOME")["CODEX_HOME"] != profile {
		t.Fatal("isolator replaced provider profile")
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, `mcp_servers.proofbridge={enabled=false`) || strings.Contains(args, "mcp_servers.codex_bridge") || strings.Contains(args, "mcp_servers.unrelated") {
		t.Fatalf("MCP isolation is not precise: %s", args)
	}
	for _, path := range paths {
		if !strings.Contains(args, "{path="+quoteTomlString(path)+",enabled=false}") {
			t.Fatalf("skill %s not disabled in %s", path, args)
		}
	}
	if !strings.Contains(args, `{path="/existing/disabled/SKILL.md",enabled=false}`) {
		t.Fatal("existing disabled skill setting lost")
	}
	if !strings.Contains(args, `{name="unrelated-skill",enabled=false}`) {
		t.Fatal("existing name-based skill rule lost")
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatal("real provider config rewritten")
	}
}
func TestManagedWorkerClaudePreservesApprovalHelperAndLogin(t *testing.T) {
	cmd := exec.Command("claude", "--mcp-config", "/tmp/approval.json", "--permission-prompt-tool", "mcp__codex_bridge__browser_approval", "--resume", "session")
	cmd.Env = []string{"CLAUDE_CONFIG_DIR=/private/login", "ANTHROPIC_MODEL=keep"}
	before := append([]string(nil), cmd.Args...)
	if err := isolateAutomationEntrypoint(cmd, "claude", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cmd.Args[3:], before[1:]) {
		t.Fatalf("Claude arguments changed: %#v", cmd.Args)
	}
	if cmd.Args[1] != "--disallowedTools" || cmd.Args[2] != "mcp__proofbridge__*,Skill(proofbridge)" {
		t.Fatalf("bad deny list: %#v", cmd.Args)
	}
	if commandEnvValues(cmd, "CLAUDE_CONFIG_DIR")["CLAUDE_CONFIG_DIR"] != "/private/login" {
		t.Fatal("Claude login profile replaced")
	}
}

// Opt-in local metadata probe. This starts the installed CLI, never a model
// turn, and only uses temporary CLI homes. CI fixtures cannot stand in for it.
func TestManagedWorkerRealCodexMetadata(t *testing.T) {
	if os.Getenv("PROOFBRIDGE_TEST_REAL_CODEX") != "1" {
		t.Skip("set PROOFBRIDGE_TEST_REAL_CODEX=1 for installed CLI metadata probe")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("official-default-home", func(t *testing.T) { testManagedWorkerCodexMetadata(t, binary, true) })
	t.Run("private-preset-home", func(t *testing.T) { testManagedWorkerCodexMetadata(t, binary, false) })
}
func testManagedWorkerCodexMetadata(t *testing.T, binary string, official bool) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", "")
	var reqEnv []string
	if !official {
		profile = filepath.Join(home, "private-preset")
		reqEnv = []string{"CODEX_HOME=" + profile}
	}
	project := filepath.Join(home, "project")
	skill := filepath.Join(project, ".agents", "skills", "proofbridge", "SKILL.md")
	writeAutomationFixture(t, skill, "---\nname: proofbridge\ndescription: ProofBridge metadata test skill\n---\nUse ProofBridge.\n")
	// An unavailable endpoint is safe because the managed override disables it.
	writeAutomationFixture(t, filepath.Join(profile, "config.toml"), "[mcp_servers.proofbridge]\nurl=\"http://127.0.0.1:9/mcp\"\n[mcp_servers.unrelated]\ncommand=\"unrelated-helper\"\n[[skills.config]]\nname=\"unrelated-skill\"\nenabled=false\n")
	otherSkill := filepath.Join(project, ".agents", "skills", "unrelated-skill", "SKILL.md")
	writeAutomationFixture(t, otherSkill, "---\nname: unrelated-skill\ndescription: Unrelated test Skill\n---\nTest.\n")
	cfg := config.Default()
	cfg.Bridge.CodexPath = binary
	cfg.Bridge.CWD = project
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runner := NewCodexAppServerRunner(&cfg)
	client, err := runner.start(ctx, RunnerRequest{CWD: project, ManagedWorker: true, OfficialLogin: official, Env: reqEnv})
	if err != nil {
		t.Fatal(err)
	}
	defer client.closeWithTimeout(2 * time.Second)
	if _, err = client.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "proofbridge-isolation-test", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}); err != nil {
		t.Fatal(client.withDiagnostics(err))
	}
	raw, err := client.request(ctx, "skills/list", map[string]any{"cwds": []string{project}, "forceReload": true})
	if err != nil {
		t.Fatal(client.withDiagnostics(err))
	}
	data, err := json.Marshal(appServerResultMap(raw))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Data []struct {
			Skills []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"skills"`
		} `json:"data"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	found := false
	otherFound := false
	for _, entry := range result.Data {
		for _, item := range entry.Skills {
			if item.Name == "unrelated-skill" {
				otherFound = true
				if item.Enabled {
					t.Fatal("unrelated disabled skill setting lost")
				}
			}
			if item.Name == "proofbridge" {
				found = true
				if item.Enabled {
					t.Fatalf("project skill remains enabled: path from skills/list; overrides=%#v", client.cmd.Args)
				}
			}
		}
	}
	if !otherFound {
		t.Fatal("unrelated skill was not discovered")
	}
	if !found {
		t.Fatalf("probe failed to discover disabled project skill: %s", data)
	}
	raw, err = client.request(ctx, "config/read", map[string]any{"includeLayers": false})
	if err != nil {
		t.Fatal(err)
	}
	parsed := appServerResultMap(raw)
	configMap, _ := parsed["config"].(map[string]any)
	servers, _ := configMap["mcp_servers"].(map[string]any)
	outer, _ := servers["proofbridge"].(map[string]any)
	otherMCP, _ := servers["unrelated"].(map[string]any)
	if otherMCP["command"] != "unrelated-helper" {
		t.Fatal("unrelated MCP config changed")
	}
	if outer["enabled"] != false {
		t.Fatalf("outer MCP remains enabled: %#v", outer)
	}
	t.Log("installed Codex metadata confirmed project Skill and proofbridge MCP disabled; no model turn requested")
}
