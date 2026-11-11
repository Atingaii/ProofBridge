package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestOfficialCodexModelPagesUseNativeCapabilitiesAndPagination(t *testing.T) {
	calls := 0
	models, err := readOfficialCodexModelPages(context.Background(), func(_ context.Context, method string, params any) (json.RawMessage, error) {
		calls++
		p := params.(map[string]any)
		if method != "model/list" || p["includeHidden"] != false || p["limit"] != officialModelPageLimit {
			t.Fatalf("directory RPC = %s %#v", method, p)
		}
		if calls == 1 {
			return json.RawMessage(`{"data":[{"model":"native-a","displayName":"Native A","supportedReasoningEfforts":[{"reasoningEffort":"ultra"}],"defaultReasoningEffort":"ultra","isDefault":true},{"model":"hidden-model","hidden":true}],"nextCursor":"page-2"}`), nil
		}
		if p["cursor"] != "page-2" {
			t.Fatalf("missing pagination cursor: %#v", p)
		}
		return json.RawMessage(`{"data":[{"model":"native-b","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}],"defaultReasoningEffort":"low"}]}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(models) != 2 || models[0].Model != "native-a" || !models[0].IsDefault || models[0].DefaultReasoningEffort != "ultra" || !reflect.DeepEqual(models[0].SupportedReasoningEfforts, []string{"ultra"}) {
		t.Fatalf("native directory = %#v, calls=%d", models, calls)
	}
}

func TestOfficialCodexModelPagesRejectMalformedOrUnboundedDirectory(t *testing.T) {
	for _, tc := range []struct {
		name string
		page string
	}{
		{"empty", `{"data":[]}`},
		{"invalid-json", `{`},
		{"invalid-model", `{"data":[{"model":""}]}`},
		{"duplicate-model", `{"data":[{"model":"a"},{"model":"a"}]}`},
		{"invalid-default", `{"data":[{"model":"a","defaultReasoningEffort":"high","supportedReasoningEfforts":[{"reasoningEffort":"low"}]}]}`},
		{"looping-cursor", `{"data":[],"nextCursor":"same"}`},
		{"oversized-page", strings.Repeat(" ", officialModelPageBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readOfficialCodexModelPages(context.Background(), func(context.Context, string, any) (json.RawMessage, error) {
				return json.RawMessage(tc.page), nil
			})
			if err == nil {
				t.Fatal("invalid directory accepted")
			}
		})
	}
	calls := 0
	_, err := readOfficialCodexModelPages(context.Background(), func(context.Context, string, any) (json.RawMessage, error) {
		calls++
		return json.Marshal(map[string]any{"data": []any{}, "nextCursor": strings.Repeat("n", calls)})
	})
	if err == nil || calls != officialModelPageCount {
		t.Fatalf("unbounded pages: calls=%d error=%v", calls, err)
	}
	_, err = readOfficialCodexModelPages(context.Background(), func(context.Context, string, any) (json.RawMessage, error) {
		return nil, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestOfficialCodexDirectoryRPCDoesNotDecryptOrChangeConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	authPath := filepath.Join(codexHome, "auth.json")
	for path, contents := range map[string]string{configPath: "model = \"operator-default\"\n", authPath: "operator-identity\n"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scriptPath := filepath.Join(home, "native-directory")
	script := `#!/usr/bin/env python3
import json, sys
assert 'model_provider="openai"' in sys.argv
assert 'forced_login_method="chatgpt"' in sys.argv
for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get('method')
    assert method in ('initialize', 'model/list'), method
    result = {} if method == 'initialize' else {'data': [{'model': 'native-a', 'supportedReasoningEfforts': [{'reasoningEffort':'ultra'}], 'defaultReasoningEffort':'ultra'}]}
    print(json.dumps({'id':msg['id'], 'result':result}), flush=True)
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = scriptPath
	cfg.Bridge.CWD = home
	// No private key is present: secret decryption would necessarily fail.
	m := &cliConfigManager{cfg: &cfg}
	result := m.handle(context.Background(), protocol.TypeCLIConfigOfficialModels, protocol.CLIConfigRequest{
		RequestID: "native-directory", CLI: "codex", Secret: protocol.EncryptedSecret{Ciphertext: "must-not-decrypt"},
	})
	if !result.OK || result.RequestID != "native-directory" || result.ConfigChanged || len(result.OfficialModels) != 1 || result.OfficialModels[0].Model != "native-a" {
		t.Fatalf("directory result = %#v", result)
	}
	for path, want := range map[string]string{configPath: "model = \"operator-default\"\n", authPath: "operator-identity\n"} {
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != want {
			t.Fatalf("operator configuration changed: %s %q %v", path, contents, err)
		}
	}
	result = m.handle(context.Background(), protocol.TypeCLIConfigOfficialModels, protocol.CLIConfigRequest{CLI: "claude"})
	if result.OK || result.Error == "" {
		t.Fatalf("Claude official directory accepted: %#v", result)
	}
}
