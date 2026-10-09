package bridge

import (
	"encoding/json"
	"fmt"
	"github.com/tencent/codex-bridge/internal/protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func recoveryFixture(t *testing.T, home, id, cwd, model string, at int64) {
	t.Helper()
	day := filepath.Join(home, "sessions", time.Unix(at, 0).UTC().Format("2006/01/02"))
	if err := os.MkdirAll(day, 0700); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(at, 0).UTC().Format(time.RFC3339)
	raw := fmt.Sprintf("{\"type\":\"session_meta\",\"timestamp\":%q,\"payload\":{\"id\":%q,\"cwd\":%q,\"model\":%q}}\n", stamp, id, cwd, model)
	raw += fmt.Sprintf("{\"type\":\"event_msg\",\"timestamp\":%q,\"payload\":{\"type\":\"token_count\",\"info\":{\"last_token_usage\":{\"input_tokens\":198150,\"cached_input_tokens\":145920,\"output_tokens\":2709}}}}\n", stamp)
	if err := os.WriteFile(filepath.Join(day, "rollout-"+id+".jsonl"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPlanningRecoveryEvidenceBoundaries(t *testing.T) {
	const at int64 = 1789020766
	base := protocol.OrchestrationUsageRecovery{WorkerSlot: "codex-a", CWD: "/workspace", StartedAt: at, EndedAt: at + 600, InputTokens: 198150, CacheReadTokens: 145920, OutputTokens: 2709}
	for _, name := range []string{"match", "cwd", "time", "tokens", "ambiguous", "model", "known", "size", "invalid-slot"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			hint := base
			cwd, model, stamp := "/workspace", "actual-model", at
			switch name {
			case "cwd":
				cwd = "/another"
			case "time":
				stamp = at - 121
			case "tokens":
				hint.InputTokens++
			case "model":
				model = ""
			case "invalid-slot":
				hint.WorkerSlot = "claude-a"
			}
			recoveryFixture(t, home, "planning-session", cwd, model, stamp)
			if name == "ambiguous" {
				recoveryFixture(t, home, "another-session", cwd, model, stamp)
			}
			if name == "size" {
				path := filepath.Join(home, "sessions", time.Unix(at, 0).UTC().Format("2006/01/02"), "rollout-planning-session.jsonl")
				if err := os.Truncate(path, 65<<20); err != nil {
					t.Fatal(err)
				}
			}
			var known []protocol.OrchestrationUsageSession
			if name == "known" {
				known = []protocol.OrchestrationUsageSession{{CLI: "codex", SessionID: "planning-session", WorkerSlot: "codex-b"}}
			}
			result := recoverPlanningUsageSessions([]protocol.OrchestrationUsageRecovery{hint}, known)
			if name != "match" {
				if len(result) != 0 {
					t.Fatal(result)
				}
				return
			}
			if len(result) != 1 || result[0].WorkerSlot != "codex-a" {
				t.Fatal(result)
			}
			request := protocol.OrchestrationUsageSyncRequest{RunID: "historical", Recovery: []protocol.OrchestrationUsageRecovery{hint}}
			// Exercise the additive wire shape and normal sync result end-to-end.
			raw, _ := json.Marshal(request)
			var decoded protocol.OrchestrationUsageSyncRequest
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			sync := scanOrchestrationUsage(decoded)
			if sync.Status != "complete" || len(sync.Events) != 1 || sync.Events[0].InputTokens != 52230 || sync.Events[0].Model != "actual-model" {
				t.Fatalf("%+v", sync)
			}
		})
	}
}

func TestPlanningRecoveryMarkerAndLocalDate(t *testing.T) {
	const at int64 = 1789020766
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	hint := protocol.OrchestrationUsageRecovery{WorkerSlot: "codex-a", CWD: "/workspace", StartedAt: at, EndedAt: at + 600, InputTokens: 198150, CacheReadTokens: 145920, OutputTokens: 2709}
	recoveryFixture(t, home, "planning-session", "/workspace", "actual-model", at)
	// Local timezone date differs from UTC even far from UTC midnight.
	from := filepath.Join(home, "sessions", time.Unix(at, 0).UTC().Format("2006/01/02"))
	to := filepath.Join(home, "sessions", time.Unix(at-86400, 0).UTC().Format("2006/01/02"))
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	result := scanOrchestrationUsage(protocol.OrchestrationUsageSyncRequest{RunID: "historical", Recovery: []protocol.OrchestrationUsageRecovery{hint}})
	if len(result.Sessions) != 2 || len(result.Events) != 1 || result.Status != "complete" {
		t.Fatalf("%+v", result)
	}
	if result.Sessions[0].SessionID != hint.RecoveryMarkerID() || result.Sessions[0].EventCount != 0 {
		t.Fatal("marker contributes usage", result.Sessions)
	}
	hint.InputTokens++
	result = scanOrchestrationUsage(protocol.OrchestrationUsageSyncRequest{RunID: "historical", Recovery: []protocol.OrchestrationUsageRecovery{hint}})
	if len(result.Sessions) != 1 || result.Sessions[0].Status != "unavailable" || result.Status == "complete" || len(result.Events) != 0 {
		t.Fatalf("failure not recorded: %+v", result)
	}
}
