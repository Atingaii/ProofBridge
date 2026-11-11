package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func officialParametersPayload() protocol.OrchestrationStartPayload {
	return protocol.OrchestrationStartPayload{RunID: "official-parameters", WorkerPair: protocol.WorkerPairCodexCodex, WorkerProfiles: map[string]protocol.WorkerProfileBinding{
		"codex-a": {CLI: "codex", OfficialLogin: true, Model: "native-a", ReasoningEffort: "high", ReasoningLevels: []string{"low", "high"}},
		"codex-b": {CLI: "codex", OfficialLogin: true, Model: "native-b", ReasoningEffort: "low", ReasoningLevels: []string{"low", "high"}},
	}}
}

func TestOfficialWorkerRuntimeAndExecOverridesStaySlotLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := NewOrchestrationManager(&config.Config{})
	defer m.CloseAll()
	payload := officialParametersPayload()
	session := m.nativeSession(payload.RunID, home)
	for slot, binding := range payload.WorkerProfiles {
		runtime, err := m.workerRuntime(payload, slot, "codex", session)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.dir != "" || len(runtime.env) != 0 || runtime.model != binding.Model || runtime.reasoningEffort != binding.ReasoningEffort || runtime.fingerprint == "" || !runtime.officialLogin {
			t.Fatalf("official runtime = %#v", runtime)
		}
		for _, thread := range []string{"", "original-thread"} {
			args := strings.Join(m.codexOrchestrationArgs(payload, slot, thread), " ")
			if !strings.Contains(args, "--model "+binding.Model) || !strings.Contains(args, "model_reasoning_effort="+quoteTomlString(binding.ReasoningEffort)) || !strings.Contains(args, "model_provider=\"openai\"") || !strings.Contains(args, "forced_login_method=\"chatgpt\"") {
				t.Fatalf("%s exec/resume override = %s", slot, args)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex-bridge", "orchestration-profiles")); !os.IsNotExist(err) {
		t.Fatalf("official runtime created private home: %v", err)
	}
	bad := payload.WorkerProfiles["codex-a"]
	bad.ReasoningEffort = "unsupported"
	payload.WorkerProfiles["codex-a"] = bad
	if _, err := m.workerRuntime(payload, "codex-a", "codex", session); err == nil {
		t.Fatal("official unsupported effort accepted")
	}
}

func TestOfficialCodexNativeSessionsForwardParametersAndResumeChangedSlot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tracePath := filepath.Join(home, "native-trace.jsonl")
	scriptPath := filepath.Join(home, "codex-native")
	traceJSON, _ := json.Marshal(tracePath)
	script := fmt.Sprintf(`#!/usr/bin/env python3
import json, sys
trace = %s
def record(obj):
    with open(trace, 'a') as f:
        f.write(json.dumps(obj) + '\n')
def emit(obj):
    print(json.dumps(obj), flush=True)
record({'argv':sys.argv[1:]})
assert 'model_provider="openai"' in sys.argv
assert 'forced_login_method="chatgpt"' in sys.argv
effective_model = None
for line in sys.stdin:
    msg = json.loads(line)
    record(msg)
    p = msg.get('params') or {}
    method = msg.get('method')
    if method == 'initialize':
        result = {}
    elif method in ('thread/start','thread/resume'):
        # Native resume skips persisted model/effort metadata when a provider
        # override is present, even if the new binding clears both values.
        assert p.get('modelProvider') == 'openai'
        effective_model = p.get('model') or 'operator-default'
        thread = p.get('threadId') or 'thread-' + effective_model
        result = {'thread':{'id':thread}, 'model':effective_model}
    elif method == 'turn/start':
        assert p.get('model') in ('native-a','native-b',None)
        assert effective_model == (p.get('model') or 'operator-default')
        if p.get('effort'):
            assert 'model_reasoning_effort="' + p['effort'] + '"' in sys.argv
        else:
            assert not any('model_reasoning_effort=' in arg for arg in sys.argv)
        emit({'id':msg['id'], 'result':{'turn':{'id':'turn-1'}}})
        emit({'method':'item/agentMessage/delta','params':{'threadId':p['threadId'],'turnId':'turn-1','itemId':'message','delta':'done'}})
        emit({'method':'turn/completed','params':{'threadId':p['threadId'],'turn':{'id':'turn-1','status':'completed'}}})
        continue
    else:
        result = {}
    emit({'id':msg['id'],'result':result})
`, traceJSON)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = scriptPath
	cfg.Bridge.CWD = home
	m := NewOrchestrationManager(&cfg)
	defer m.CloseAll()
	payload := officialParametersPayload()
	state := &orchestrationSessionState{NativeSession: m.nativeSession(payload.RunID, home), CodexThreadIDs: make(map[string]string)}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for index, slot := range []string{"codex-a", "codex-b", "codex-a"} {
		content, _, thread, _, err := m.runCodexInteractive(ctx, payload, fmt.Sprint(index), "worker", slot, "test", state)
		if err != nil || content != "done" || thread != "thread-"+payload.WorkerProfiles[slot].Model {
			t.Fatalf("%s native turn = %q thread=%q err=%v", slot, content, thread, err)
		}
	}
	// A's changed default is a concrete native effort and resumes its existing
	// thread. B's independent binding and resident process remain intact.
	binding := payload.WorkerProfiles["codex-a"]
	binding.ReasoningEffort = "low"
	payload.WorkerProfiles["codex-a"] = binding
	_, _, thread, _, err := m.runCodexInteractive(ctx, payload, "changed", "worker", "codex-a", "test", state)
	if err != nil || thread != "thread-native-a" {
		t.Fatalf("changed slot did not resume original thread: %q %v", thread, err)
	}
	binding.ReasoningEffort = ""
	binding.ReasoningLevels = nil
	payload.WorkerProfiles["codex-a"] = binding
	_, _, thread, _, err = m.runCodexInteractive(ctx, payload, "native-default", "worker", "codex-a", "test", state)
	if err != nil || thread != "thread-native-a" {
		t.Fatalf("model-only default did not resume original thread: %q %v", thread, err)
	}
	// Clearing the model as well must restore CLI defaults without importing
	// model/effort metadata from the old native thread. Identity still survives.
	binding.Model = ""
	payload.WorkerProfiles["codex-a"] = binding
	_, _, thread, _, err = m.runCodexInteractive(ctx, payload, "all-defaults", "worker", "codex-a", "test", state)
	if err != nil || thread != "thread-native-a" {
		t.Fatalf("cleared defaults did not resume original thread: %q %v", thread, err)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	starts, resumes, processStarts := 0, 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(trace)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["argv"] != nil {
			processStarts++
		}
		if record["method"] == "thread/start" {
			starts++
		}
		if record["method"] == "thread/resume" {
			resumes++
			p := record["params"].(map[string]any)
			if p["threadId"] != "thread-native-a" {
				t.Fatal(p)
			}
		}
	}
	if starts != 2 || resumes != 3 || processStarts != 5 {
		t.Fatalf("native continuity starts=%d resumes=%d processes=%d trace=%s", starts, resumes, processStarts, trace)
	}
}

func TestOfficialCodexSilentVerifierUsesAssignedSlotParameters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "operator-home"))
	scriptPath := filepath.Join(home, "silent-verifier")
	script := `#!/usr/bin/env python3
import json, sys
assert '--model' in sys.argv and sys.argv[sys.argv.index('--model')+1] == 'native-b'
assert 'model_reasoning_effort="low"' in sys.argv
assert 'model_provider="openai"' in sys.argv
assert 'forced_login_method="chatgpt"' in sys.argv
assert __import__('os').environ['CODEX_HOME'].endswith('operator-home')
sys.stdin.read()
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'verified'}}))
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = scriptPath
	cfg.Bridge.CWD = home
	m := NewOrchestrationManager(&cfg)
	defer m.CloseAll()
	output, err := m.runSilentVerifierCLI(context.Background(), officialParametersPayload(), orchestrationTurnAssignment{CLI: "codex", WorkerSlot: "codex-b"}, "test", home)
	if err != nil || output.Content != "verified" {
		t.Fatalf("official verifier = %#v, %v", output, err)
	}
}

func TestOfficialThreadProviderOverrideDoesNotChangeUnboundRunner(t *testing.T) {
	cfg := config.Default()
	runner := NewCodexAppServerRunner(&cfg)
	for _, req := range []RunnerRequest{{}, {Model: "provider-model", ReasoningEffort: "high"}} {
		if _, ok := runner.threadStartParams(req)["modelProvider"]; ok {
			t.Fatal("unbound thread acquired official provider")
		}
		if _, ok := runner.threadResumeParams("original-thread", req)["modelProvider"]; ok {
			t.Fatal("unbound resume acquired official provider")
		}
	}
	req := RunnerRequest{OfficialLogin: true}
	if runner.threadStartParams(req)["modelProvider"] != "openai" || runner.threadResumeParams("original-thread", req)["modelProvider"] != "openai" {
		t.Fatal("official defaults did not override persisted provider settings")
	}
}
