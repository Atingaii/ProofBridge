package bridge

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

func effectiveCodexUsageHome(home string) string {
	if value := os.Getenv("CODEX_HOME"); value != "" {
		return value
	}
	return filepath.Join(home, ".codex")
}

// The first session's identity can be lost on cancellation in old Bridges.
// Search only date directories around the recorded start, never workspace
// files or other CLI homes. Incomplete/ambiguous scans produce no attribution.
func recoverPlanningUsageSessions(hints []protocol.OrchestrationUsageRecovery, known []protocol.OrchestrationUsageSession) []protocol.OrchestrationUsageSession {
	sessions, _ := recoverPlanningUsage(hints, known)
	return sessions
}

func recoverPlanningUsage(hints []protocol.OrchestrationUsageRecovery, known []protocol.OrchestrationUsageSession) ([]protocol.OrchestrationUsageSession, []protocol.OrchestrationUsageSessionResult) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	root := filepath.Join(effectiveCodexUsageHome(home), "sessions")
	var recovered []protocol.OrchestrationUsageSession
	var results []protocol.OrchestrationUsageSessionResult
	seen := map[string]string{}
	for _, session := range known {
		if session.CLI == "codex" {
			seen[session.SessionID] = session.WorkerSlot
		}
	}
	var budget int64 = 64 << 20
	if len(hints) > 8 {
		hints = hints[:8]
	}
	for _, hint := range hints {
		marker := protocol.OrchestrationUsageSessionResult{CLI: "codex", WorkerSlot: hint.WorkerSlot, SessionID: hint.RecoveryMarkerID(), Status: "unavailable", Error: "invalid native recovery evidence"}
		validSlot := hint.WorkerSlot == "codex" || hint.WorkerSlot == "codex-a" || hint.WorkerSlot == "codex-b"
		if !validSlot || !filepath.IsAbs(hint.CWD) || hint.StartedAt <= 0 || hint.EndedAt < hint.StartedAt || hint.EndedAt-hint.StartedAt > 86400 || hint.InputTokens <= 0 || hint.OutputTokens <= 0 || hint.CacheReadTokens < 0 || hint.CacheReadTokens > hint.InputTokens {
			results = append(results, marker)
			continue
		}
		candidates, complete := discoverPlanningUsage(root, hint, &budget)
		switch {
		case !complete:
			marker.Error = "native recovery scan incomplete or budget exceeded"
		case len(candidates) == 0:
			marker.Error = "no native session matches the recorded planning evidence"
		case len(candidates) > 1:
			marker.Error = "multiple native sessions match the recorded planning evidence"
		default:
			session := candidates[0]
			owner, exists := seen[session.SessionID]
			if exists && owner != session.WorkerSlot {
				marker.Error = "native session has a conflicting worker assignment"
			} else {
				marker.Status, marker.Error = "complete", ""
				if !exists {
					recovered = append(recovered, session)
					seen[session.SessionID] = session.WorkerSlot
				}
			}
		}
		results = append(results, marker)
	}
	return recovered, results
}

func discoverPlanningUsage(root string, hint protocol.OrchestrationUsageRecovery, budget *int64) ([]protocol.OrchestrationUsageSession, bool) {
	var matches []protocol.OrchestrationUsageSession
	visited := map[string]bool{}
	files := 0
	for _, delta := range []int64{0, -86400, 86400} {
		day := time.Unix(hint.StartedAt+delta, 0).UTC().Format("2006/01/02")
		if visited[day] {
			continue
		}
		visited[day] = true
		entries, err := os.ReadDir(filepath.Join(root, day))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, false
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			files++
			if files > 256 {
				return nil, false
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return nil, false
			}
			path := filepath.Join(root, day, entry.Name())
			// Read just the metadata line before spending the per-sync scan budget.
			file, err := os.Open(path)
			if err != nil {
				return nil, false
			}
			raw, readErr := readJSONLLine(bufio.NewReaderSize(file, 4096), 64<<10)
			file.Close()
			*budget -= int64(len(raw))
			if *budget < 0 || readErr != nil {
				return nil, false
			}
			var meta struct {
				Type      string
				Timestamp string
				Payload   struct{ ID, CWD, Timestamp string }
			}
			if json.Unmarshal(raw, &meta) != nil || meta.Type != "session_meta" {
				continue
			}
			stamp := meta.Payload.Timestamp
			if stamp == "" {
				stamp = meta.Timestamp
			}
			at, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil || at.Unix() < hint.StartedAt-120 || at.Unix() > hint.StartedAt+120 || filepath.Clean(meta.Payload.CWD) != filepath.Clean(hint.CWD) || !validNativeSessionID(meta.Payload.ID) {
				continue
			}
			if info.Size() > 64<<20 || info.Size() > *budget {
				return nil, false
			}
			*budget -= info.Size()
			session := protocol.OrchestrationUsageSession{CLI: "codex", WorkerSlot: hint.WorkerSlot, SessionID: meta.Payload.ID}
			rows, err := parseCodexUsageLog(path, session)
			if err != nil {
				return nil, false
			}
			var input, cache, output int64
			model := ""
			valid := len(rows) > 0
			for _, row := range rows {
				if row.OccurredAt < hint.StartedAt-120 || row.OccurredAt > hint.EndedAt+2 || row.Model == "" || (model != "" && model != row.Model) {
					valid = false
					break
				}
				model = row.Model
				input += row.InputTokens + row.CacheReadTokens
				cache += row.CacheReadTokens
				output += row.OutputTokens
			}
			if valid && input == hint.InputTokens && cache == hint.CacheReadTokens && output == hint.OutputTokens {
				matches = append(matches, session)
			}
		}
	}
	return matches, true
}
