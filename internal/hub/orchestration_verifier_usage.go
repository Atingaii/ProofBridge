package hub

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

// Recover the assignment from the verdict, never from agent-number parity.
// Default-model attribution is scoped to an immutable graph and labelled as
// inherited configuration rather than native metadata from the verifier call.
func enrichVerifierUsage(events []store.OrchestrationEvent, graphs []store.OrchestrationTaskGraph) []store.OrchestrationEvent {
	type assignment struct{ Agent, Slot, CLI, Model string }
	assigned := map[string]assignment{}
	for _, event := range events {
		if event.Kind != "verifier.verdict" {
			continue
		}
		raw, _ := json.Marshal(event.Data["verifiers"])
		var agents []assignment
		if json.Unmarshal(raw, &agents) != nil {
			continue
		}
		for _, agent := range agents {
			if agent.Agent != "" && agent.Slot != "" && agent.CLI != "" {
				assigned[agent.CLI+"\x00"+event.TurnID+"-"+agent.Agent] = agent
			}
		}
	}
	ordered := append([]store.OrchestrationTaskGraph(nil), graphs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].CreatedAt < ordered[j].CreatedAt })
	graphIndex := func(event store.OrchestrationEvent) int {
		if event.Task != nil && event.Task.GraphID != "" {
			for i, graph := range ordered {
				if graph.ID == event.Task.GraphID {
					return i
				}
			}
			return -1
		}
		return sort.Search(len(ordered), func(i int) bool { return ordered[i].CreatedAt > event.CreatedAt }) - 1
	}
	out := append([]store.OrchestrationEvent(nil), events...)
	for i, event := range events {
		if event.Kind != "turn.usage" || event.Role != "verifier" {
			continue
		}
		data := make(map[string]any, len(event.Data)+3)
		for k, v := range event.Data {
			data[k] = v
		}
		cli := event.CLI
		if cli == "" {
			cli = usageText(data, "cli")
		}
		agent := assigned[cli+"\x00"+event.TurnID]
		slot := strings.TrimPrefix(usageText(data, "workerSlot"), "verifier:")
		if agent.Slot != "" {
			slot = agent.Slot
		}
		if slot == "verifier" {
			slot = ""
		}
		if slot != "" {
			data["workerSlot"] = "verifier:" + slot
		}
		model, source := usageText(data, "model"), usageText(data, "modelSource")
		if missingUsageModel(model) && !missingUsageModel(agent.Model) {
			model, source = agent.Model, "verifier-verdict"
		}
		graph := graphIndex(event)
		if missingUsageModel(model) && slot != "" && graph >= 0 {
			var payload protocol.OrchestrationStartPayload
			if json.Unmarshal([]byte(ordered[graph].PayloadJSON), &payload) == nil {
				if binding, ok := payload.WorkerProfiles[slot]; ok && (binding.CLI == "" || binding.CLI == cli) {
					if !missingUsageModel(binding.Model) {
						model, source = binding.Model, "worker-config"
					} else if binding.OfficialLogin {
						candidate, ambiguous := "", false
						for _, worker := range events {
							if worker.Kind != "turn.usage" || worker.Role == "verifier" || worker.CLI != cli || usageText(worker.Data, "workerSlot") != slot || worker.CreatedAt > event.CreatedAt || graphIndex(worker) != graph {
								continue
							}
							observed := usageText(worker.Data, "model")
							if missingUsageModel(observed) {
								continue
							}
							if candidate != "" && candidate != observed {
								ambiguous = true
								break
							}
							candidate = observed
						}
						if candidate != "" && !ambiguous {
							model, source = candidate, "worker-config"
						}
					}
				}
			}
		}
		if !missingUsageModel(model) {
			data["model"], data["modelSource"] = model, source
		}
		out[i].Data = data
	}
	return out
}
