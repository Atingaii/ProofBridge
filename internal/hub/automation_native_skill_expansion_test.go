package hub

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/automationassets"
)

const nativeSkillExpansionHelperEnv = "PROOFBRIDGE_TEST_SKILL_EXPANSION_HELPER"

// Everything here belongs to a single temporary acceptance profile. Neither
// this hook nor its configuration is installed in the user's Skill or settings.
type nativeSkillExpansionFixture struct {
	Home        string
	Project     string
	SkillPath   string
	ReceiptPath string
	Nonce       string
}

type nativeSkillExpansionReceipt struct {
	SchemaVersion int    `json:"schemaVersion"`
	Event         string `json:"event"`
	ExpansionType string `json:"expansionType"`
	CommandName   string `json:"commandName"`
	CommandSource string `json:"commandSource"`
	Nonce         string `json:"nonce"`
	FileSHA256    string `json:"fileSHA256"`
	PathSHA256    string `json:"pathSHA256"`
}

type nativeSkillExpansionObservation struct {
	ReceiptValid bool
	BodyMatched  bool
}

func nativeSkillCorrelateExpansion(e nativeSkillEvidence, observation nativeSkillExpansionObservation) nativeSkillEvidence {
	e.Loaded = e.Loaded || observation.ReceiptValid && observation.BodyMatched
	return e
}

func newNativeSkillExpansionFixture(home, project, root string) (*nativeSkillExpansionFixture, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	f := &nativeSkillExpansionFixture{
		Home: home, Project: project,
		SkillPath:   filepath.Join(home, ".claude", "skills", "proofbridge", "SKILL.md"),
		ReceiptPath: filepath.Join(root, "skill-expansion-receipt.json"), Nonce: hex.EncodeToString(nonce[:]),
	}
	if !f.validInstallation() {
		return nil, os.ErrInvalid
	}
	if _, err := os.Lstat(f.ReceiptPath); !os.IsNotExist(err) {
		return nil, os.ErrExist
	}
	return f, nil
}

func nativeSkillExpansionSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// The installed asset is static: 2.1.287 strips this frontmatter expression,
// prefixes its base directory, and only appends the supplied arguments. Stop
// observing this channel if the asset gains dynamic substitutions or commands.
func nativeSkillExpansionBody() string {
	raw := automationassets.SkillFiles["SKILL.md"]
	if strings.Contains(raw, "$") || strings.Contains(raw, "!`") || strings.Contains(raw, "```!") {
		return ""
	}
	boundary := regexp.MustCompile(`(?s)^---\s*\n.*?---\s*\n?`).FindStringIndex(raw)
	if boundary == nil {
		return ""
	}
	return raw[boundary[1]:]
}

func (f *nativeSkillExpansionFixture) validInstallation() bool {
	if !filepath.IsAbs(f.Home) || !filepath.IsAbs(f.Project) || f.SkillPath != filepath.Join(f.Home, ".claude", "skills", "proofbridge", "SKILL.md") || f.ReceiptPath != filepath.Join(filepath.Dir(f.Home), "skill-expansion-receipt.json") || len(f.Nonce) != 64 || nativeSkillExpansionBody() == "" {
		return false
	}
	if _, err := hex.DecodeString(f.Nonce); err != nil {
		return false
	}
	canonical, err := filepath.EvalSymlinks(f.SkillPath)
	if err != nil || canonical != filepath.Clean(f.SkillPath) {
		return false
	}
	info, err := os.Lstat(f.SkillPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	raw, err := os.ReadFile(f.SkillPath)
	if err != nil || nativeSkillExpansionSHA(raw) != nativeSkillExpansionSHA([]byte(automationassets.SkillFiles["SKILL.md"])) {
		return false
	}
	// command_source=userSettings does not distinguish skills from legacy
	// commands. This isolated profile must contain only the installed Skill.
	for _, directory := range []string{
		filepath.Join(f.Home, ".claude", "commands"),
		filepath.Join(f.Project, ".claude", "commands"),
		filepath.Join(f.Project, ".claude", "skills"),
	} {
		entries, err := os.ReadDir(directory)
		if err != nil && !os.IsNotExist(err) || len(entries) != 0 {
			return false
		}
	}
	entries, err := os.ReadDir(filepath.Dir(filepath.Dir(f.SkillPath)))
	return err == nil && len(entries) == 1 && entries[0].Name() == "proofbridge" && entries[0].IsDir()
}

func (f *nativeSkillExpansionFixture) hookSettings(root string) (string, string, error) {
	settings := map[string]any{"hooks": map[string]any{"UserPromptExpansion": []any{
		map[string]any{"matcher": "^proofbridge$", "hooks": []any{map[string]any{
			"type": "command", "timeout": 5,
			"command": shellQuoteAutomation(os.Args[0]) + " -test.run '^TestAutomationNativeSkillExpansionHookHelper$' -test.timeout=5s",
		}}},
	}}}
	raw, err := json.Marshal(settings)
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(root, "skill-expansion-settings.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", "", err
	}
	private, err := json.Marshal(f)
	return path, nativeSkillExpansionHelperEnv + "=" + string(private), err
}

// This is a silent command hook, not an agent hook. It never grants tools,
// blocks a prompt, adds context, or writes the incoming prompt/session ID.
func TestAutomationNativeSkillExpansionHookHelper(t *testing.T) {
	private := os.Getenv(nativeSkillExpansionHelperEnv)
	if private == "" {
		t.Skip("temporary CLI command hook only")
	}
	var f nativeSkillExpansionFixture
	if json.Unmarshal([]byte(private), &f) == nil {
		_ = f.writeReceipt(os.Stdin)
	}
	os.Exit(0)
}

func (f *nativeSkillExpansionFixture) writeReceipt(input io.Reader) bool {
	var event struct {
		Event  string `json:"hook_event_name"`
		Type   string `json:"expansion_type"`
		Name   string `json:"command_name"`
		Source string `json:"command_source"`
		CWD    string `json:"cwd"`
	}
	if json.NewDecoder(io.LimitReader(input, 64<<10)).Decode(&event) != nil || event.Event != "UserPromptExpansion" || event.Type != "slash_command" || event.Name != "proofbridge" || event.Source != "userSettings" || filepath.Clean(event.CWD) != filepath.Clean(f.Project) || !f.validInstallation() {
		return false
	}
	receipt := nativeSkillExpansionReceipt{
		SchemaVersion: 1, Event: event.Event, ExpansionType: event.Type, CommandName: event.Name, CommandSource: event.Source, Nonce: f.Nonce,
		FileSHA256: nativeSkillExpansionSHA([]byte(automationassets.SkillFiles["SKILL.md"])), PathSHA256: nativeSkillExpansionSHA([]byte(f.SkillPath)),
	}
	file, err := os.OpenFile(f.ReceiptPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	err = json.NewEncoder(file).Encode(receipt)
	closeErr := file.Close()
	return err == nil && closeErr == nil
}

func (f *nativeSkillExpansionFixture) receiptValid() bool {
	if !f.validInstallation() {
		return false
	}
	info, err := os.Lstat(f.ReceiptPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return false
	}
	raw, err := os.ReadFile(f.ReceiptPath)
	if err != nil {
		return false
	}
	var r nativeSkillExpansionReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil {
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return false
	}
	return r.SchemaVersion == 1 && r.Event == "UserPromptExpansion" && r.ExpansionType == "slash_command" && r.CommandName == "proofbridge" && r.CommandSource == "userSettings" && r.Nonce == f.Nonce && r.FileSHA256 == nativeSkillExpansionSHA([]byte(automationassets.SkillFiles["SKILL.md"])) && r.PathSHA256 == nativeSkillExpansionSHA([]byte(f.SkillPath))
}

func (f *nativeSkillExpansionFixture) observe(output []byte) nativeSkillExpansionObservation {
	observation := nativeSkillExpansionObservation{ReceiptValid: f.receiptValid()}
	if !f.validInstallation() {
		return observation
	}
	body := nativeSkillExpansionBody()
	prefix := "Base directory for this skill: " + filepath.Dir(f.SkillPath) + "\n\n"
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		var e struct {
			Type       string                `json:"type"`
			Synthetic  bool                  `json:"isSynthetic"`
			Replay     bool                  `json:"isReplay"`
			Parent     any                   `json:"parent_tool_use_id"`
			AgentID    string                `json:"agent_id"`
			ToolResult any                   `json:"tool_use_result"`
			Composed   bool                  `json:"client_composed"`
			Origin     struct{ Kind string } `json:"origin"`
			Message    struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.Type != "user" || !e.Synthetic || e.Replay || e.Parent != nil || e.AgentID != "" || e.ToolResult != nil || e.Composed || e.Origin.Kind == "human" || e.Message.Role != "user" {
			continue
		}
		var text string
		var blocks []struct{ Type, Text string }
		if json.Unmarshal(e.Message.Content, &text) == nil {
			blocks = append(blocks, struct{ Type, Text string }{"text", text})
		} else if json.Unmarshal(e.Message.Content, &blocks) != nil {
			continue
		}
		valid := true
		for _, block := range blocks {
			valid = valid && block.Type == "text"
		}
		if !valid {
			continue
		}
		for _, block := range blocks {
			if !strings.HasPrefix(block.Text, prefix) {
				continue
			}
			rendered := strings.TrimPrefix(block.Text, prefix)
			if len(rendered) >= len(body) && nativeSkillExpansionSHA([]byte(rendered[:len(body)])) == nativeSkillExpansionSHA([]byte(body)) {
				observation.BodyMatched = true
			}
		}
	}
	return observation
}

func nativeSkillExpansionTestFixture(t *testing.T) *nativeSkillExpansionFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	path := filepath.Join(home, ".claude", "skills", "proofbridge", "SKILL.md")
	for _, dir := range []string{filepath.Dir(path), project} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(automationassets.SkillFiles["SKILL.md"]), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := newNativeSkillExpansionFixture(home, project, root)
	if err != nil {
		t.Fatal("prepare expansion fixture")
	}
	return f
}

func nativeSkillExpansionTestInput(f *nativeSkillExpansionFixture) map[string]any {
	return map[string]any{
		"hook_event_name": "UserPromptExpansion", "expansion_type": "slash_command", "command_name": "proofbridge", "command_source": "userSettings", "cwd": f.Project,
		"session_id": "fixture-private-session", "prompt": "private@example.test fixture-private-key", "command_args": "private@example.test fixture-private-key",
	}
}

func nativeSkillExpansionTestOutput(f *nativeSkillExpansionFixture) map[string]any {
	return map[string]any{"type": "user", "isSynthetic": true, "parent_tool_use_id": nil,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Base directory for this skill: " + filepath.Dir(f.SkillPath) + "\n\n" + nativeSkillExpansionBody() + "\n\nARGUMENTS: fixture arguments"}}},
	}
}

func nativeSkillExpansionJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAutomationNativeSkillExpansionRequiresClientReceiptAndExactBody(t *testing.T) {
	f := nativeSkillExpansionTestFixture(t)
	output := nativeSkillExpansionJSON(t, nativeSkillExpansionTestOutput(f))
	before := f.observe(output)
	if before.ReceiptValid || !before.BodyMatched {
		t.Fatal("a body without the trusted hook must remain separate evidence")
	}
	input := nativeSkillExpansionJSON(t, nativeSkillExpansionTestInput(f))
	if !f.writeReceipt(bytes.NewReader(input)) {
		t.Fatal("valid command expansion receipt was not written")
	}
	after := f.observe(output)
	if !after.ReceiptValid || !after.BodyMatched {
		t.Fatal("valid installed source and client-rendered content did not correlate")
	}
	for _, tc := range []struct {
		observation nativeSkillExpansionObservation
		want        string
	}{
		{before, "fail"}, {nativeSkillExpansionObservation{ReceiptValid: true}, "fail"}, {after, "pass"},
	} {
		e := nativeSkillCorrelateExpansion(nativeSkillEvidence{ReplyObserved: true}, tc.observation)
		calls := map[string]bool{"proofbridge_get_context": true, "proofbridge_list_endpoints": true}
		if nativeSkillProbeStatus(e, calls, nil, nil) != tc.want {
			t.Fatal("expansion loading requires both correlated observations")
		}
		if nativeSkillProbeStatus(e, nil, nil, nil) == "pass" || nativeSkillProbeStatus(nativeSkillCorrelateExpansion(nativeSkillEvidence{}, tc.observation), calls, nil, nil) == "pass" {
			t.Fatal("expansion receipt cannot replace tools or a terminal reply")
		}
	}
	withoutBody := f.observe([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"/proofbridge loaded"}]}}`))
	if !withoutBody.ReceiptValid || withoutBody.BodyMatched {
		t.Fatal("the pre-expansion hook alone must not prove content loading")
	}
	raw, err := os.ReadFile(f.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private@example.test", "fixture-private-key", "fixture-private-session", "prompt", "session_id", f.SkillPath, f.Project} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatal("private receipt retained an unapproved value")
		}
	}
}

func TestAutomationNativeSkillExpansionRejectsUntrustedContent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"assistant-self-report", func(e map[string]any) { e["type"] = "assistant" }},
		{"ordinary-human-input", func(e map[string]any) { e["isSynthetic"] = false }},
		{"human-origin", func(e map[string]any) { e["origin"] = map[string]any{"kind": "human"} }},
		{"verbatim-input", func(e map[string]any) { e["client_composed"] = true }},
		{"old-replay", func(e map[string]any) { e["isReplay"] = true }},
		{"subagent", func(e map[string]any) { e["parent_tool_use_id"] = "fixture-subagent" }},
		{"agent-result", func(e map[string]any) { e["agent_id"] = "fixture-agent" }},
		{"structured-tool-result", func(e map[string]any) { e["tool_use_result"] = map[string]any{} }},
		{"discovery-list", func(e map[string]any) { e["type"] = "system"; e["subtype"] = "init" }},
		{"tool-result-block", func(e map[string]any) {
			m := e["message"].(map[string]any)
			blocks := m["content"].([]any)
			blocks[0].(map[string]any)["type"] = "tool_result"
		}},
		{"wrong-path", func(e map[string]any) {
			blocks := e["message"].(map[string]any)["content"].([]any)
			block := blocks[0].(map[string]any)
			block["text"] = strings.Replace(block["text"].(string), "Base directory for this skill: ", "Base directory for this skill: /impostor", 1)
		}},
		{"altered-body", func(e map[string]any) {
			blocks := e["message"].(map[string]any)["content"].([]any)
			block := blocks[0].(map[string]any)
			block["text"] = strings.Replace(block["text"].(string), "# ProofBridge", "# Impostor", 1)
		}},
		{"prefix-only", func(e map[string]any) {
			blocks := e["message"].(map[string]any)["content"].([]any)
			block := blocks[0].(map[string]any)
			text := block["text"].(string)
			block["text"] = text[:strings.Index(text, "# ProofBridge")+len("# ProofBridge")]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := nativeSkillExpansionTestFixture(t)
			if !f.writeReceipt(bytes.NewReader(nativeSkillExpansionJSON(t, nativeSkillExpansionTestInput(f)))) {
				t.Fatal("prepare valid receipt")
			}
			output := nativeSkillExpansionTestOutput(f)
			tc.change(output)
			if f.observe(nativeSkillExpansionJSON(t, output)).BodyMatched {
				t.Fatal("untrusted or incomplete content was accepted as a client expansion")
			}
		})
	}
}

func TestAutomationNativeSkillExpansionRejectsWrongHookIdentityAndCollision(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"hook_event_name", "UserPromptSubmit"}, {"expansion_type", "mcp_prompt"},
		{"command_name", "CodexBridge"}, {"command_source", "plugin"}, {"command_source", "projectSettings"}, {"command_source", "user"}, {"cwd", "/impostor"},
	} {
		t.Run(tc.field+"-"+tc.value, func(t *testing.T) {
			f := nativeSkillExpansionTestFixture(t)
			input := nativeSkillExpansionTestInput(f)
			input[tc.field] = tc.value
			if f.writeReceipt(bytes.NewReader(nativeSkillExpansionJSON(t, input))) || f.receiptValid() {
				t.Fatal("wrong hook source, name or request identity accepted")
			}
		})
	}
	for _, scope := range []string{"home", "project"} {
		t.Run("legacy-command-"+scope, func(t *testing.T) {
			f := nativeSkillExpansionTestFixture(t)
			base := f.Home
			if scope == "project" {
				base = f.Project
			}
			path := filepath.Join(base, ".claude", "commands", "proofbridge.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("same-name legacy command"), 0o600); err != nil {
				t.Fatal(err)
			}
			if f.writeReceipt(bytes.NewReader(nativeSkillExpansionJSON(t, nativeSkillExpansionTestInput(f)))) {
				t.Fatal("a same-source custom command made Skill identity ambiguous")
			}
		})
	}
}

func TestAutomationNativeSkillExpansionRejectsForgedStaleOrChangedReceipt(t *testing.T) {
	for _, field := range []string{"Nonce", "FileSHA256", "PathSHA256", "CommandName", "CommandSource"} {
		t.Run(field, func(t *testing.T) {
			f := nativeSkillExpansionTestFixture(t)
			if !f.writeReceipt(bytes.NewReader(nativeSkillExpansionJSON(t, nativeSkillExpansionTestInput(f)))) {
				t.Fatal("prepare receipt")
			}
			raw, _ := os.ReadFile(f.ReceiptPath)
			var r nativeSkillExpansionReceipt
			_ = json.Unmarshal(raw, &r)
			switch field {
			case "Nonce":
				r.Nonce = strings.Repeat("0", 64)
			case "FileSHA256":
				r.FileSHA256 = strings.Repeat("0", 64)
			case "PathSHA256":
				r.PathSHA256 = strings.Repeat("0", 64)
			case "CommandName":
				r.CommandName = "other"
			case "CommandSource":
				r.CommandSource = "plugin"
			}
			if err := os.WriteFile(f.ReceiptPath, nativeSkillExpansionJSON(t, r), 0o600); err != nil {
				t.Fatal(err)
			}
			if f.receiptValid() {
				t.Fatal("stale or forged receipt was accepted")
			}
		})
	}
	f := nativeSkillExpansionTestFixture(t)
	input := nativeSkillExpansionJSON(t, nativeSkillExpansionTestInput(f))
	if !f.writeReceipt(bytes.NewReader(input)) || f.writeReceipt(bytes.NewReader(input)) {
		t.Fatal("receipt must be created once and never overwritten")
	}
	if err := os.WriteFile(f.SkillPath, []byte("changed Skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if f.observe(nativeSkillExpansionJSON(t, nativeSkillExpansionTestOutput(f))).ReceiptValid {
		t.Fatal("changed installed file was accepted")
	}
}

func TestAutomationNativeSkillExpansionRejectsUnsafeFilesystemAndReceiptShape(t *testing.T) {
	for _, attack := range []string{"receipt-symlink", "receipt-public", "extra-fields", "trailing-json", "skill-symlink"} {
		t.Run(attack, func(t *testing.T) {
			f := nativeSkillExpansionTestFixture(t)
			if !f.writeReceipt(bytes.NewReader(nativeSkillExpansionJSON(t, nativeSkillExpansionTestInput(f)))) {
				t.Fatal("prepare valid receipt")
			}
			raw, _ := os.ReadFile(f.ReceiptPath)
			switch attack {
			case "receipt-symlink":
				target := f.ReceiptPath + ".target"
				if err := os.Rename(f.ReceiptPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, f.ReceiptPath); err != nil {
					t.Fatal(err)
				}
			case "receipt-public":
				if err := os.Chmod(f.ReceiptPath, 0o644); err != nil {
					t.Fatal(err)
				}
			case "extra-fields":
				var r map[string]any
				_ = json.Unmarshal(raw, &r)
				r["prompt"] = "fixture-private-prompt"
				if err := os.WriteFile(f.ReceiptPath, nativeSkillExpansionJSON(t, r), 0o600); err != nil {
					t.Fatal(err)
				}
			case "trailing-json":
				if err := os.WriteFile(f.ReceiptPath, append(raw, []byte(`{}`)...), 0o600); err != nil {
					t.Fatal(err)
				}
			case "skill-symlink":
				target := f.SkillPath + ".target"
				if err := os.Rename(f.SkillPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, f.SkillPath); err != nil {
					t.Fatal(err)
				}
			}
			if f.receiptValid() {
				t.Fatal("unsafe file identity or receipt format accepted")
			}
		})
	}
}

func TestAutomationNativeSkillExpansionCommandHelperIsSilentAndPrivate(t *testing.T) {
	f := nativeSkillExpansionTestFixture(t)
	_, env, err := f.hookSettings(filepath.Dir(f.Home))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestAutomationNativeSkillExpansionHookHelper$", "-test.timeout=5s")
	cmd.Env = append(os.Environ(), env)
	cmd.Stdin = bytes.NewReader(nativeSkillExpansionJSON(t, nativeSkillExpansionTestInput(f)))
	output, err := cmd.CombinedOutput()
	if err != nil || len(output) != 0 || !f.receiptValid() {
		t.Fatal("temporary command helper must write only its private receipt and produce no stdout/stderr")
	}
}
