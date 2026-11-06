package hub

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tencent/codex-bridge/internal/store"
)

// This projection contains execution choices only. Encrypted worker credentials,
// native resume identities and task text never belong in a configuration card.
func (s *Server) orchestrationExecutionConfiguration(ctx context.Context, uid string, run store.OrchestrationRun) (json.RawMessage, string, error) {
	sub, err := s.store.LatestAutomationSubmissionForRun(ctx, uid, run.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, "", err
	}
	submissionID := sub.ID
	var saved map[string]json.RawMessage
	if len(sub.Configuration) > 0 {
		_ = json.Unmarshal(sub.Configuration, &saved)
	}
	if len(saved) > 0 {
		safe := map[string]json.RawMessage{}
		for _, key := range []string{"schemaVersion", "endpointId", "agentId", "cwd", "workerPair", "mode", "firstCli", "profile", "verificationMode", "executionMode", "maxTurns", "monitoringMode", "nativeContextCompaction"} {
			if value, ok := saved[key]; ok {
				safe[key] = value
			}
		}
		var workers map[string]map[string]json.RawMessage
		if json.Unmarshal(saved["workers"], &workers) == nil {
			safeWorkers := map[string]map[string]json.RawMessage{}
			for slot, worker := range workers {
				fields := map[string]json.RawMessage{}
				for _, key := range []string{"cli", "nativeDefault", "presetId", "name", "officialLogin", "model", "reasoningEffort"} {
					if value, ok := worker[key]; ok {
						fields[key] = value
					}
				}
				safeWorkers[slot] = fields
			}
			safe["workers"], _ = json.Marshal(safeWorkers)
		}
		raw, e := json.Marshal(safe)
		return raw, submissionID, e
	}
	bindings, err := s.store.OrchestrationWorkerProfiles(ctx, run.ID)
	if err != nil {
		return nil, "", err
	}
	workers := map[string]any{}
	for slot, cli := range automationWorkerSlots(run.WorkerPair) {
		if b, ok := bindings[slot]; ok {
			workers[slot] = map[string]any{"cli": cli, "nativeDefault": false, "presetId": b.PresetID, "name": b.Name, "officialLogin": b.OfficialLogin, "model": b.Model, "reasoningEffort": b.ReasoningEffort}
		} else {
			workers[slot] = map[string]any{"cli": cli, "nativeDefault": true}
		}
	}
	raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "endpointId": run.AgentID, "cwd": run.CWD, "workerPair": run.WorkerPair, "mode": run.Mode, "firstCli": run.FirstCLI, "profile": run.Profile, "verificationMode": run.VerificationMode, "executionMode": run.ExecutionMode, "maxTurns": run.MaxTurns, "nativeContextCompaction": run.NativeContextCompaction, "workers": workers})
	return raw, submissionID, err
}
