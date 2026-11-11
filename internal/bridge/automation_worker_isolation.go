package bridge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const managedWorkerEnvironment = "PROOFBRIDGE_MANAGED_WORKER=1"

type workerSkillRule struct {
	Name    string `toml:"name"`
	Path    string `toml:"path"`
	Enabled *bool  `toml:"enabled"`
}

// isolateAutomationEntrypoint alters this child only. It neither rewrites the
// user's configuration nor disables unrelated MCP servers or approval helpers.
func isolateAutomationEntrypoint(cmd *exec.Cmd, cli, cwd string) error {
	if cmd == nil {
		return nil
	}
	replaceCommandEnv(cmd, managedWorkerEnvironment)
	var overrides []string
	if cli == "claude" {
		overrides = []string{"--disallowedTools", "mcp__proofbridge__*,Skill(proofbridge)"}
	} else {
		// Supplying a disabled, structurally valid entry also works when this
		// private CLI home never had a proofbridge MCP entry.
		overrides = []string{"-c", `mcp_servers.proofbridge={enabled=false,url="http://127.0.0.1:9/mcp"}`}
		rules, err := managedWorkerSkillRules(cmd, cwd)
		if err != nil {
			return err
		}
		if len(rules) > 0 {
			values := make([]string, 0, len(rules))
			for _, rule := range rules {
				enabled := true
				if rule.Enabled != nil {
					enabled = *rule.Enabled
				}
				selector := "path=" + quoteTomlString(rule.Path)
				if rule.Name != "" {
					selector = "name=" + quoteTomlString(rule.Name)
				}
				values = append(values, "{"+selector+",enabled="+strconv.FormatBool(enabled)+"}")
			}
			overrides = append(overrides, "-c", "skills.config=["+strings.Join(values, ",")+"]")
		}
	}
	// Codex subcommands have their own config override list. Place these flags
	// beside the existing subcommand flags, or a later provider -c can discard
	// top-level overrides (verified with installed app-server metadata).
	position := 1
	if cli != "claude" && len(cmd.Args) > 1 {
		position = 2
		if cmd.Args[1] == "exec" && len(cmd.Args) > 2 && cmd.Args[2] == "resume" {
			position = 3
		}
	}
	prefix := append([]string(nil), cmd.Args[:position]...)
	cmd.Args = append(append(prefix, overrides...), cmd.Args[position:]...)
	return nil
}

func managedWorkerSkillRules(cmd *exec.Cmd, cwd string) ([]workerSkillRule, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	env := commandEnvValues(cmd, "HOME", "CODEX_HOME")
	if env["HOME"] != "" {
		home = env["HOME"]
	}
	codeHome := env["CODEX_HOME"]
	if codeHome == "" {
		codeHome = filepath.Join(home, ".codex")
	}
	roots := []string{}
	if cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return nil, err
		}
		for parent := abs; ; parent = filepath.Dir(parent) {
			roots = append(roots, parent)
			if filepath.Dir(parent) == parent {
				break
			}
		}
		for i, j := 0, len(roots)-1; i < j; i, j = i+1, j-1 {
			roots[i], roots[j] = roots[j], roots[i]
		}
	}
	configFiles := []string{filepath.Join(codeHome, "config.toml")}
	for _, root := range roots {
		configFiles = append(configFiles, filepath.Join(root, ".codex", "config.toml"))
	}
	var rules []workerSkillRule
	seenConfig := map[string]bool{}
	for _, file := range configFiles {
		if seenConfig[file] {
			continue
		}
		seenConfig[file] = true
		info, err := os.Stat(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read managed worker skill configuration: %w", err)
		}
		if info.Size() > 2<<20 {
			return nil, fmt.Errorf("managed worker configuration exceeds 2 MiB")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var config struct {
			Skills struct {
				Config []workerSkillRule `toml:"config"`
			} `toml:"skills"`
		}
		if err := toml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("invalid skill configuration in %s", file)
		}
		for _, rule := range config.Skills.Config {
			if rule.Path != "" || rule.Name != "" {
				rules = append(rules, rule)
			}
		}
	}
	candidates := []string{
		filepath.Join(home, ".agents", "skills", "proofbridge", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "proofbridge", "SKILL.md"),
		filepath.Join(codeHome, "skills", "proofbridge", "SKILL.md"),
	}
	for _, root := range roots {
		candidates = append(candidates, filepath.Join(root, ".agents", "skills", "proofbridge", "SKILL.md"), filepath.Join(root, ".codex", "skills", "proofbridge", "SKILL.md"))
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			disabled := false
			rules = append(rules, workerSkillRule{Path: path, Enabled: &disabled})
			if canonical, err := filepath.EvalSymlinks(path); err == nil && canonical != path {
				rules = append(rules, workerSkillRule{Path: canonical, Enabled: &disabled})
			}
		}
	}
	disabled := false
	rules = append(rules, workerSkillRule{Name: "proofbridge", Enabled: &disabled})
	return rules, nil
}
