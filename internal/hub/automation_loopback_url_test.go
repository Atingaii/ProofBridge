package hub

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/store"
)

func TestAutomationLoopbackMCPKeepsLANResultOrigin(t *testing.T) {
	s, _, uid, _ := newOrchestrationTestServer(t)
	s.cfg.Automation.PublicURL = "http://10.0.0.10:8088/"
	s.cfg.Automation.MCPURL = "http://127.0.0.1:8088/"
	access := authedJSON(t, s, uid, "GET", "/api/automation/access", nil, 200)
	if access["mcpUrl"] != "http://127.0.0.1:8088/mcp" {
		t.Fatal("MCP endpoint lost its loopback origin")
	}
	for _, field := range []string{"codexConfig", "claudeConfig"} {
		copied := access[field].(string)
		if !strings.Contains(copied, "http://127.0.0.1:8088/mcp") || strings.Contains(copied, "10.0.0.10") {
			t.Fatal("copied MCP config contains the browser origin")
		}
	}
	// Result links use the browser origin, including in MCP responses.
	result, err := s.automationSubmissionResponse(context.Background(), uid, store.AutomationSubmission{RunID: "loopback-fixture"}, false)
	if err != nil || result.(map[string]any)["resultUrl"] != "http://10.0.0.10:8088/orchestrate/runs/loopback-fixture" {
		t.Fatal("result link lost the LAN origin")
	}
	// Actual MCP SDK transport remains usable through a loopback listener.
	server := httptest.NewServer(s.httpSrv.Handler)
	defer server.Close()
	session := automationSDKClient(t, server.URL+"/mcp", access["token"].(string))
	callAutomationSDK(t, session, "get_context", map[string]any{}, false)
	callAutomationSDK(t, session, "list_endpoints", map[string]any{}, false)
	s.cfg.Automation.MCPURL = "https://client.example.test"
	s.cfg.Automation.PublicURL = "https://browser.example.test"
	changed := authedJSON(t, s, uid, "GET", "/api/automation/access", nil, 200)
	if changed["token"] != access["token"] || changed["mcpUrl"] != "https://client.example.test/mcp" {
		t.Fatal("URL change rotated token or failed to apply")
	}
	s.cfg.Automation.MCPURL = ""
	fallback := authedJSON(t, s, uid, "GET", "/api/automation/access", nil, 200)
	if fallback["mcpUrl"] != "https://browser.example.test/mcp" {
		t.Fatal("default HTTPS behavior changed")
	}
}

func TestAutomationMCPOverrideAndLANURLBoundaries(t *testing.T) {
	s, _, uid, _ := newOrchestrationTestServer(t)
	s.cfg.Automation.PublicURL = "https://browser.example.test"
	for _, raw := range []string{"http://10.0.0.10:8088", "http://203.0.113.10:8088", "http://lab.test", "https://user:pass@lab.test", "https://lab.test?token=x", "https://lab.test/#fragment", "file:///tmp/mcp"} {
		s.cfg.Automation.MCPURL = raw
		authedJSON(t, s, uid, "GET", "/api/automation/access", nil, 503)
	}
	for _, raw := range []string{"http://127.0.0.1:8088", "http://localhost:8088", "http://[::1]:8088", "https://lab.test"} {
		s.cfg.Automation.MCPURL = raw
		if _, err := s.automationMCPURL(); err != nil {
			t.Fatalf("valid MCP override rejected: %s", raw)
		}
	}
	for _, tc := range []struct {
		public, mcp string
		allowed     bool
	}{
		{"http://10.0.0.10:8088", "", false},
		{"http://10.0.0.10:8088", "http://127.0.0.1:8088", true},
		{"http://[fd00::10]:8088", "http://[::1]:8088", true},
		{"http://10.0.0.10:8088", "https://remote.test", false},
		{"http://10.0.0.10:8088", "http://10.0.0.11:8088", false},
		{"http://203.0.113.10:8088", "http://127.0.0.1:8088", false},
		{"http://unresolved.test:8088", "http://127.0.0.1:8088", false},
	} {
		s.cfg.Automation.PublicURL, s.cfg.Automation.MCPURL = tc.public, tc.mcp
		_, err := s.automationPublicURL()
		if (err == nil) != tc.allowed {
			t.Fatalf("unexpected browser URL acceptance: %+v", tc)
		}
	}
}
