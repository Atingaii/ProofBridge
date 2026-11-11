package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

func Load(configDir string) (*Config, error) {
	lookup, err := configurationEnvironment()
	if err != nil {
		return nil, err
	}
	getEnv := func(key string) string { value, _ := lookup(key); return value }
	if v := getEnv("CODEX_BRIDGE_CONFIG_DIR"); v != "" {
		configDir = v
	}
	cfg := Default()

	env := getEnv("APP_ENV")
	if env == "" {
		env = "dev"
	}
	cfg.App.Env = env

	path := filepath.Join(configDir, env+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		example := path + ".example"
		data, err = os.ReadFile(example)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read config %s: %w", example, err)
		}
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	}

	applyEnvWithLookup(&cfg, lookup)
	return &cfg, nil
}

func applyEnv(cfg *Config) { applyEnvWithLookup(cfg, os.LookupEnv) }

func applyEnvWithLookup(cfg *Config, lookup envLookup) {
	getEnv := func(key string) string { value, _ := lookup(key); return value }
	applyAutomationEnvWithLookup(cfg, lookup)
	if v := getEnv("APP_ENV"); v != "" {
		cfg.App.Env = v
	}
	if v := getEnv("APP_DEBUG"); v != "" {
		cfg.App.Debug = parseBool(v, cfg.App.Debug)
	}
	if v := getEnv("APP_HOST"); v != "" {
		cfg.Gateway.Host = v
	}
	if v := getEnv("APP_PORT"); v != "" {
		cfg.Gateway.Port = parseInt(v, cfg.Gateway.Port)
	}
	if v := getEnv("HUB_DB_PATH"); v != "" {
		cfg.Hub.DBPath = v
	}
	if v := getEnv("HUB_COOKIE_SECURE"); v != "" {
		cfg.Hub.CookieSecure = parseBool(v, cfg.Hub.CookieSecure)
	}
	if v := getEnv("HUB_BRIDGE_DOWNLOAD_URL"); v != "" {
		cfg.Hub.BridgeDownloadURL = v
	}
	if v := getEnv("HUB_BROWSER_LEASE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Hub.BrowserLeaseTTL = Duration{Duration: d}
		}
	}
	if v := getEnv("JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := getEnv("HUB_USERNAME"); v != "" {
		cfg.Auth.BootstrapUsername = v
	}
	if v := getEnv("HUB_PASSWORD"); v != "" {
		cfg.Auth.BootstrapPassword = v
	}
	if v := getEnv("REGISTRATION_ENABLED"); v != "" {
		cfg.Auth.Registration.Enabled = parseBool(v, cfg.Auth.Registration.Enabled)
	}
	if v := getEnv("REGISTRATION_REQUIRE_TURNSTILE"); v != "" {
		cfg.Auth.Registration.RequireTurnstile = parseBool(v, cfg.Auth.Registration.RequireTurnstile)
	}
	if v := getEnv("TURNSTILE_SITE_KEY"); v != "" {
		cfg.Auth.Registration.TurnstileSiteKey = v
	}
	if v := getEnv("TURNSTILE_SECRET"); v != "" {
		cfg.Auth.Registration.TurnstileSecret = v
	}
	if v := getEnv("TURNSTILE_HOSTNAME"); v != "" {
		cfg.Auth.Registration.TurnstileHostname = v
	}
	if v := getEnv("BRIDGE_HUB_URL"); v != "" {
		cfg.Bridge.HubURL = v
	}
	if v := getEnv("BRIDGE_TOKEN"); v != "" {
		cfg.Bridge.Token = v
	}
	if v := getEnv("BRIDGE_TOKEN_FILE"); v != "" {
		cfg.Bridge.TokenFile = v
	}
	if v := getEnv("BRIDGE_NAME"); v != "" {
		cfg.Bridge.Name = v
	}
	if v := getEnv("BRIDGE_MACHINE_ID_FILE"); v != "" {
		cfg.Bridge.MachineIDFile = v
	}
	if v := getEnv("BRIDGE_CWD"); v != "" {
		cfg.Bridge.CWD = v
	}
	if v := getEnv("BRIDGE_RUNNER"); v != "" {
		cfg.Bridge.Runner = v
	}
	if v := getEnv("BRIDGE_CODEX_PATH"); v != "" {
		cfg.Bridge.CodexPath = v
	}
	if v := getEnv("BRIDGE_CLAUDE_PATH"); v != "" {
		cfg.Bridge.ClaudePath = v
	}
	if v := getEnv("BRIDGE_CLAUDE_MODEL"); v != "" {
		cfg.Bridge.ClaudeModel = v
	}
	if v := getEnv("BRIDGE_CLAUDE_EFFORT"); v != "" {
		cfg.Bridge.ClaudeEffort = v
	}
	if v := getEnv("BRIDGE_MODEL"); v != "" {
		cfg.Bridge.Model = v
	}
	if v := getEnv("BRIDGE_SANDBOX"); v != "" {
		cfg.Bridge.Sandbox = v
	}
	if v := getEnv("BRIDGE_APPROVAL_POLICY"); v != "" {
		cfg.Bridge.ApprovalPolicy = v
	}
	if v := getEnv("BRIDGE_STRICT_WORKSPACE"); v != "" {
		cfg.Bridge.StrictWorkspace = parseBool(v, cfg.Bridge.StrictWorkspace)
	}
	if v := getEnv("BRIDGE_STRICT_WORKSPACE_READ_ONLY"); v != "" {
		cfg.Bridge.StrictWorkspaceReadOnly = filepath.SplitList(v)
	}
	if v := getEnv("BRIDGE_DURABLE_TASK_GRAPH"); v != "" {
		cfg.Bridge.DurableTaskGraph = parseBool(v, cfg.Bridge.DurableTaskGraph)
	}
	if v := getEnv("BRIDGE_ACP_CLI"); v != "" {
		cfg.Bridge.ACP.CLI = v
	}
	if v := getEnv("BRIDGE_ACP_CLAUDE_COMMAND"); v != "" {
		cfg.Bridge.ACP.ClaudeCommand = v
	}
	if v := getEnv("BRIDGE_ACP_CODEX_COMMAND"); v != "" {
		cfg.Bridge.ACP.CodexCommand = v
	}
	if v := getEnv("BRIDGE_ACP_PREFER_NATIVE_RESUME"); v != "" {
		cfg.Bridge.ACP.PreferNativeResume = parseBool(v, cfg.Bridge.ACP.PreferNativeResume)
	}
	if v := getEnv("BRIDGE_LONG_COMMAND_OBSERVER_ENABLED"); v != "" {
		cfg.Bridge.LongCommandObserver.Enabled = parseBool(v, cfg.Bridge.LongCommandObserver.Enabled)
	}
	if v := getEnv("BRIDGE_LONG_COMMAND_OBSERVER_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Bridge.LongCommandObserver.After = Duration{Duration: d}
		}
	}
	if v := getEnv("LOG_LEVEL"); v != "" {
		cfg.Observability.LogLevel = v
	}
	if v := getEnv("LOG_FORMAT"); v != "" {
		cfg.Observability.LogFormat = v
	}
}

func parseInt(value string, fallback int) int {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func parseBool(value string, fallback bool) bool {
	v, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return v
}
