package hub

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

// Retry the two legacy identity bugs on a historical page read. Healthy or
// recently attempted syncs do not cause another filesystem scan.
func historicalUsageSyncDue(syncs []store.OrchestrationUsageSync, now int64) bool {
	if len(syncs) == 0 {
		return true
	}
	legacy, latest := false, int64(0)
	for _, sync := range syncs {
		if sync.ScannedAt > latest {
			latest = sync.ScannedAt
		}
		if sync.Status == "unavailable" && (sync.Error == "isolated worker runtime is no longer available" || sync.Error == "unsupported CLI") {
			legacy = true
		}
	}
	return legacy && now-latest >= 6*60*60
}

// Enrichment is a read projection: never mutate persisted events or use a
// currently edited preset to describe an old call.
func enrichOrchestrationUsageTimeline(events []store.OrchestrationEvent, graphs []store.OrchestrationTaskGraph) []store.OrchestrationEvent {
	type identity struct {
		slot, model string
		round       int
	}
	turns := map[string]identity{}
	ordered := append([]store.OrchestrationTaskGraph(nil), graphs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].CreatedAt < ordered[j].CreatedAt })
	type binding struct {
		at       int64
		profiles map[string]protocol.WorkerProfileBinding
	}
	var bindings []binding
	byGraph := map[string]map[string]protocol.WorkerProfileBinding{}
	for _, graph := range ordered {
		var p protocol.OrchestrationStartPayload
		if json.Unmarshal([]byte(graph.PayloadJSON), &p) == nil {
			bindings = append(bindings, binding{graph.CreatedAt, p.WorkerProfiles})
			byGraph[graph.ID] = p.WorkerProfiles
		}
	}
	for _, e := range events {
		if e.Kind != "turn.start" || e.TurnID == "" {
			continue
		}
		id := identity{slot: usageText(e.Data, "workerSlot"), model: usageText(e.Data, "model"), round: usageRoundNumber(e.Data)}
		if e.TurnStartData != nil {
			id = identity{e.TurnStartData.WorkerSlot, e.TurnStartData.Model, e.TurnStartData.Round}
		}
		if e.Task != nil {
			if id.slot == "" && e.Role != "verifier" {
				id.slot = e.Task.WorkerSlot
			}
			if id.round == 0 {
				id.round = e.Task.Round
			}
		}
		if missingUsageModel(id.model) && id.slot != "" && e.Role != "verifier" {
			var profiles map[string]protocol.WorkerProfileBinding
			if e.Task != nil && e.Task.GraphID != "" {
				profiles = byGraph[e.Task.GraphID]
			} else {
				for _, b := range bindings {
					if b.at <= e.CreatedAt {
						profiles = b.profiles
					}
				}
			}
			id.model = profiles[id.slot].Model
		}
		turns[e.CLI+"\x00"+e.TurnID] = id
	}
	// Native identity survives canceled tasks that never emit a run.end. Do not
	// infer a model when one turn reports conflicting models (e.g. retries).
	models, conflicts := map[string]string{}, map[string]bool{}
	for _, e := range events {
		if e.Kind != "bridge.note" || usageText(e.Data, "category") != "native-identity" {
			continue
		}
		key, model := e.CLI+"\x00"+e.TurnID, usageText(e.Data, "model")
		if missingUsageModel(model) {
			continue
		}
		if old := models[key]; old != "" && old != model {
			conflicts[key] = true
		}
		models[key] = model
	}
	for key, model := range models {
		id := turns[key]
		if conflicts[key] {
			id.model = ""
		} else {
			id.model = model
		}
		turns[key] = id
	}
	out := append([]store.OrchestrationEvent(nil), events...)
	for i, e := range out {
		if e.Kind != "turn.usage" {
			continue
		}
		data := make(map[string]any, len(e.Data)+3)
		for k, v := range e.Data {
			data[k] = v
		}
		cli := e.CLI
		if cli == "" {
			cli = usageText(data, "cli")
		}
		id := turns[cli+"\x00"+e.TurnID]
		if e.Task != nil {
			if id.slot == "" && e.Role != "verifier" {
				id.slot = e.Task.WorkerSlot
			}
			if id.round == 0 {
				id.round = e.Task.Round
			}
		}
		if usageText(data, "workerSlot") == "" {
			data["workerSlot"] = id.slot
		}
		if e.Role == "verifier" {
			slot := usageText(data, "workerSlot")
			if slot == "" {
				slot = "verifier"
			}
			if slot != "verifier" && !strings.HasPrefix(slot, "verifier:") {
				slot = "verifier:" + slot
			}
			data["workerSlot"] = slot
		}
		if missingUsageModel(usageText(data, "model")) && !missingUsageModel(id.model) {
			data["model"] = id.model
		}
		if usageRoundNumber(data) == 0 && id.round > 0 {
			data["round"] = id.round
		}
		out[i].Data = data
	}
	return out
}

// The ledger can repair model identity without replacing a more complete
// snapshot (e.g. one that also includes verifier/provider billing). Only one
// concrete native model in the exact worker/turn interval is sufficient proof.
func enrichSnapshotModelsFromLedger(events []store.OrchestrationEvent, ledger []protocol.OrchestrationUsageEvent) []store.OrchestrationEvent {
	starts := map[string]int64{}
	for _, e := range events {
		if e.Kind == "turn.start" {
			starts[e.CLI+"\x00"+e.TurnID] = e.CreatedAt
		}
	}
	for i, e := range events {
		if e.Kind != "turn.usage" || !missingUsageModel(usageText(e.Data, "model")) || e.Role == "verifier" {
			continue
		}
		start, ok := starts[e.CLI+"\x00"+e.TurnID]
		if !ok {
			continue
		}
		slot := usageText(e.Data, "workerSlot")
		if slot == "" {
			continue
		}
		model := ""
		ambiguous := false
		for _, row := range ledger {
			if row.CLI != e.CLI || row.WorkerSlot != slot || row.OccurredAt < start || row.OccurredAt > e.CreatedAt || missingUsageModel(row.Model) {
				continue
			}
			if model != "" && model != row.Model {
				ambiguous = true
				break
			}
			model = row.Model
		}
		if model != "" && !ambiguous {
			events[i].Data["model"] = model
		}
	}
	return events
}

func workerLedgerRound(events []store.OrchestrationEvent, boundaries []orchestrationRoundBoundary, usage protocol.OrchestrationUsageEvent) int {
	round, latest := 0, int64(0)
	for _, e := range events {
		if e.Kind != "turn.start" || e.CLI != usage.CLI || e.CreatedAt > usage.OccurredAt || e.CreatedAt < latest {
			continue
		}
		slot, n := usageText(e.Data, "workerSlot"), usageRoundNumber(e.Data)
		if e.TurnStartData != nil {
			slot, n = e.TurnStartData.WorkerSlot, e.TurnStartData.Round
		}
		if e.Task != nil {
			if slot == "" {
				slot = e.Task.WorkerSlot
			}
			if n == 0 {
				n = e.Task.Round
			}
		}
		if slot != "" && slot == usage.WorkerSlot && n > 0 {
			round, latest = n, e.CreatedAt
		}
	}
	if round > 0 {
		return round
	}
	return orchestrationRoundForTime(boundaries, usage.OccurredAt)
}

func usageText(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return strings.TrimSpace(s)
}
func missingUsageModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", "unknown", "default", "codex", "claude":
		return true
	}
	return false
}

func appendWorkerUsage(items *[]orchestrationUsageStats, usage orchestrationUsageStats) {
	if usage.WorkerSlot == "" {
		usage.WorkerSlot = "unknown"
	}
	for i := range *items {
		item := &(*items)[i]
		if item.CLI != usage.CLI || item.WorkerSlot != usage.WorkerSlot || item.Model != usage.Model {
			continue
		}
		if item.ModelSource == "worker-config" || usage.ModelSource == "worker-config" {
			item.ModelSource = "worker-config"
		} else if item.ModelSource != usage.ModelSource {
			item.ModelSource = "mixed"
		}
		item.InputTokens += usage.InputTokens
		item.OutputTokens += usage.OutputTokens
		item.CacheReadTokens += usage.CacheReadTokens
		item.CacheWriteTokens += usage.CacheWriteTokens
		item.ReasoningTokens += usage.ReasoningTokens
		item.CallCount += usage.CallCount
		item.EstimatedCostUSD += usage.EstimatedCostUSD
		item.CostKnown = item.CostKnown && usage.CostKnown
		item.Native = item.Native || usage.Native
		item.Estimated = item.Estimated || usage.Estimated
		if item.CostSource != usage.CostSource {
			item.CostSource = "mixed"
		}
		return
	}
	*items = append(*items, usage)
}

func appendParticipantRound(stats *orchestrationRunStats, rounds map[int]*orchestrationRoundStats, round int, usage orchestrationUsageStats) {
	if round <= 0 {
		round = 1
	}
	appendWorkerUsage(&stats.ByWorker, usage)
	appendWorkerUsage(&rounds[round].ByWorker, usage)
	rounds[round].EstimatedCostUSD += usage.EstimatedCostUSD
}
