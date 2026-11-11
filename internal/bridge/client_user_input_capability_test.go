package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func claudeVersionFixture(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+output+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeUserInputCapabilityRequiresMinimumVersionAndMode(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		strict        bool
		auto          bool
		want          bool
	}{
		{name: "minimum", version: "2.0.71 (Claude Code)", want: true},
		{name: "before headless default", version: "2.0.70 (Claude Code)"},
		{name: "minimum prerelease", version: "2.0.71-beta.1 (Claude Code)"},
		{name: "minimum build metadata", version: "2.0.71+build.1 (Claude Code)", want: true},
		{name: "review required", version: "2.1.281 (Claude Code)", want: true},
		{name: "auto execute", version: "2.1.281 (Claude Code)", auto: true, want: true},
		{name: "newer patch", version: "2.1.282 (Claude Code)", want: true},
		{name: "future minor", version: "2.2.0 (Claude Code)", want: true},
		{name: "future major", version: "3.0.0 (Claude Code)", want: true},
		{name: "interactive only", version: "2.0.21 (Claude Code)"},
		{name: "unknown version", version: "development (Claude Code)"},
		{name: "spoofed prefix", version: "2.1.281 (Claude Code) extra"},
		{name: "strict workspace", version: "2.1.281 (Claude Code)", strict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Bridge.ClaudePath = claudeVersionFixture(t, tc.version)
			cfg.Bridge.CodexPath = filepath.Join(t.TempDir(), "missing-codex")
			cfg.Bridge.StrictWorkspace = tc.strict
			if tc.auto {
				cfg.Bridge.ApprovalPolicy = "never"
				cfg.Bridge.Sandbox = "danger-full-access"
			} else {
				cfg.Bridge.ApprovalPolicy = "on-request"
				cfg.Bridge.Sandbox = "workspace-write"
			}
			capability := BridgeCapabilities(&cfg).NativeUserInput["claude"]
			if capability.Version != 3 || capability.Available != tc.want || capability.Blocking != tc.want {
				t.Fatalf("capability = %#v, want available=%v", capability, tc.want)
			}
			manager := NewOrchestrationManager(&cfg)
			server, cleanup, err := manager.prepareClaudeApprovalServer(context.Background(), protocol.OrchestrationStartPayload{RunID: "run", NativeUserInput: true, NativeUserInputProtocolVersion: 3}, "turn", "worker", "claude-b")
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if tc.want && (server == nil || server.userInput.manager == nil) {
				t.Fatal("supported CLI did not enable the project helper")
			}
			if !tc.want && server != nil && server.userInput.manager != nil {
				t.Fatal("unsupported CLI enabled native question routing")
			}
			if tc.want {
				server.updateTurn("next-turn", "reviewer")
				if server.userInput.workerID != "claude-b" || server.userInput.turnID != "next-turn" || server.requester.role != "reviewer" {
					t.Fatalf("role change lost native worker identity: %#v", server.userInput)
				}
			}
			// An old Hub may set the boolean after seeing a v3 Bridge, but
			// cannot attest to the new reconciliation contract.
			legacy, legacyCleanup, err := manager.prepareClaudeApprovalServer(context.Background(), protocol.OrchestrationStartPayload{RunID: "legacy", NativeUserInput: true}, "turn", "worker", "claude-b")
			if err != nil {
				t.Fatal(err)
			}
			legacyCleanup()
			if legacy != nil && legacy.userInput.manager != nil {
				t.Fatal("old true-only Hub payload enabled native questions")
			}
		})
	}
}

func TestCodexUserInputCapabilityRequiresMinimumVersionAndMode(t *testing.T) {
	for _, tc := range []struct {
		name, version      string
		strict, auto, want bool
	}{
		{name: "minimum", version: "codex-cli 0.147.0", want: true},
		{name: "before explicit blocking", version: "codex-cli 0.146.1"},
		{name: "minimum prerelease", version: "codex-cli 0.147.0-alpha.1"},
		{name: "previously tested", version: "codex-cli 0.156.1", want: true},
		{name: "installed", version: "codex-cli 0.158.0", want: true},
		{name: "auto execute", version: "codex-cli 0.158.0", auto: true, want: true},
		{name: "future minor", version: "codex-cli 0.200.0", want: true},
		{name: "future major", version: "codex-cli 1.0.0", want: true},
		{name: "future prerelease", version: "codex-cli 0.159.0-alpha.1", want: true},
		{name: "build metadata", version: "codex-cli 0.158.0+local.1", want: true},
		{name: "numeric comparison", version: "codex-cli 0.99.0"},
		{name: "wrong prefix", version: "codex 0.158.0"},
		{name: "extra output", version: "codex-cli 0.158.0 extra"},
		{name: "missing component", version: "codex-cli 0.158"},
		{name: "unknown", version: "codex-cli dev"},
		{name: "strict workspace", version: "codex-cli 0.158.0", strict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Bridge.CodexPath = claudeVersionFixture(t, tc.version)
			cfg.Bridge.ClaudePath = filepath.Join(t.TempDir(), "missing-claude")
			cfg.Bridge.StrictWorkspace = tc.strict
			cfg.Bridge.ApprovalPolicy = "on-request"
			cfg.Bridge.Sandbox = "workspace-write"
			if tc.auto {
				cfg.Bridge.ApprovalPolicy = "never"
				cfg.Bridge.Sandbox = "danger-full-access"
			}
			capability := BridgeCapabilities(&cfg).NativeUserInput["codex"]
			if capability.Version != 3 || capability.Available != tc.want || capability.Blocking != tc.want || capability.NonBlocking != tc.want {
				t.Fatalf("capability = %#v, want available=%v", capability, tc.want)
			}
			if !tc.want && capability.Reason == "" {
				t.Fatal("unsupported CLI has no explanation")
			}
			manager := NewOrchestrationManager(&cfg)
			payload := protocol.OrchestrationStartPayload{NativeUserInput: true, NativeUserInputProtocolVersion: 3}
			if got := manager.codexNativeUserInputEnabled(payload); got != tc.want {
				t.Fatalf("runtime enabled = %v, want %v", got, tc.want)
			}
			payload.NativeUserInputProtocolVersion = 0
			if manager.codexNativeUserInputEnabled(payload) {
				t.Fatal("old Hub enabled native questions")
			}
			payload.NativeUserInputProtocolVersion = 3
			payload.NativeUserInput = false
			if manager.codexNativeUserInputEnabled(payload) {
				t.Fatal("disabled payload enabled native questions")
			}
		})
	}
}

func TestUserInputVersionRejectsFailedVersionCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli")
	for _, version := range []string{"codex-cli 0.158.0", "2.1.281 (Claude Code)"} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\nexit 1\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if supportedCodexUserInputVersion(path) || supportedClaudeUserInputVersion(path) {
			t.Fatal("failed version command enabled questions")
		}
	}
}
