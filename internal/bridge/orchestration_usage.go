package bridge

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/tencent/codex-bridge/internal/usagepricing"
)

// Native counts are preserved; missing data is never estimated from text.
// ByModel is internal only: emitTurnUsage emits its disjoint parts using the
// existing event shape, rather than emitting both parts and their aggregate.
type orchestrationUsage struct {
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
	Estimated        bool
	Native           bool
	CostKnown        bool
	CostSource       string
	PricingModel     string
	ByModel          []orchestrationUsage
}

func estimateOrchestrationUsage(model, prompt, content string) orchestrationUsage {
	return orchestrationUsage{Model: strings.TrimSpace(model), Estimated: true, CostSource: "unavailable"}
}

func (m *OrchestrationManager) recordNativeUsage(turnID, cli, model string, raw json.RawMessage) {
	usage, ok := normalizeNativeUsage(cli, model, raw)
	if !ok {
		return
	}
	m.mu.Lock()
	m.nativeUsage[turnID] = append(m.nativeUsage[turnID], usage)
	m.mu.Unlock()
}

func (m *OrchestrationManager) orchestrationUsageForTurn(turnID, model, prompt, content string) orchestrationUsage {
	m.mu.Lock()
	native := m.nativeUsage[turnID]
	delete(m.nativeUsage, turnID)
	m.mu.Unlock()
	if len(native) > 0 {
		return combineModelUsage(native)
	}
	return estimateOrchestrationUsage(model, prompt, content)
}

func usageModelParts(usage orchestrationUsage) []orchestrationUsage {
	if len(usage.ByModel) > 0 {
		return append([]orchestrationUsage(nil), usage.ByModel...)
	}
	return []orchestrationUsage{usage}
}

func combineModelUsage(items []orchestrationUsage) orchestrationUsage {
	var parts []orchestrationUsage
	for _, item := range items {
		parts = append(parts, usageModelParts(item)...)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	result := orchestrationUsage{CostKnown: len(parts) > 0, ByModel: parts}
	for _, item := range parts {
		result.Model = mergeUsageLabel(result.Model, item.Model)
		result.InputTokens += item.InputTokens
		result.OutputTokens += item.OutputTokens
		result.CacheReadTokens += item.CacheReadTokens
		result.CacheWriteTokens += item.CacheWriteTokens
		result.EstimatedCostUSD += item.EstimatedCostUSD
		result.Estimated = result.Estimated || item.Estimated
		result.Native = result.Native || item.Native
		result.CostKnown = result.CostKnown && item.CostKnown
		result.CostSource = mergeUsageLabel(result.CostSource, item.CostSource)
		result.PricingModel = mergeUsageLabel(result.PricingModel, item.PricingModel)
	}
	return result
}

func mergeUsageLabel(current, next string) string {
	if current == "" {
		return next
	}
	if next != "" && current != next {
		return "mixed"
	}
	return current
}

func normalizeNativeUsage(cli, model string, raw json.RawMessage) (orchestrationUsage, bool) {
	var outer map[string]any
	if json.Unmarshal(raw, &outer) != nil {
		return orchestrationUsage{}, false
	}
	if response, ok := outer["response"].(map[string]any); ok {
		outer = response
	}
	// Actual provider model IDs override configured aliases, which can refer to
	// a different model after provider-side routing or fallback.
	if actual := firstString(outer, "model"); actual != "" {
		model = actual
	}
	if models, ok := outer["modelUsage"].(map[string]any); ok && len(models) > 0 {
		names := make([]string, 0, len(models))
		for name := range models {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]orchestrationUsage, 0, len(names))
		missingModelCounts := false
		for _, name := range names {
			entry, ok := models[name].(map[string]any)
			if !ok {
				continue
			}
			counts := nativeUsageCounts(name, entry)
			missingModelCounts = missingModelCounts || !orchestrationUsageHasCounts(counts)
			if !orchestrationUsageHasCounts(counts) && len(names) == 1 {
				// Older Claude versions supplied per-model cost but only aggregate tokens.
				counts = nativeUsageCounts(name, unwrapNativeUsage(outer))
			}
			priceNativeUsage(cli, &counts, entry)
			if orchestrationUsageHasCounts(counts) || counts.CostKnown {
				parts = append(parts, counts)
			}
		}
		if len(parts) > 0 {
			if len(names) > 1 && missingModelCounts {
				// Some providers expose per-model costs but aggregate-only tokens.
				// Preserve any unallocated counts without guessing their model.
				remainder := nativeUsageCounts("unattributed", unwrapNativeUsage(outer))
				for _, part := range parts {
					remainder.InputTokens -= part.InputTokens
					remainder.OutputTokens -= part.OutputTokens
					remainder.CacheReadTokens -= part.CacheReadTokens
					remainder.CacheWriteTokens -= part.CacheWriteTokens
				}
				remainder.InputTokens = max(0, remainder.InputTokens)
				remainder.OutputTokens = max(0, remainder.OutputTokens)
				remainder.CacheReadTokens = max(0, remainder.CacheReadTokens)
				remainder.CacheWriteTokens = max(0, remainder.CacheWriteTokens)
				if orchestrationUsageHasCounts(remainder) {
					remainder.CostSource = "unavailable"
					parts = append(parts, remainder)
				}
			}
			// A provider total is authoritative when it belongs to a single model.
			if len(names) == 1 {
				applyProviderUsageCost(&parts[0], outer)
			} else {
				parts = preserveMultiModelProviderTotal(parts, outer)
			}
			return combineModelUsage(parts), true
		}
	}
	usage := nativeUsageCounts(model, unwrapNativeUsage(outer))
	priceNativeUsage(cli, &usage, outer)
	return usage, orchestrationUsageHasCounts(usage) || usage.CostKnown
}

// A provider may report a total bill without splitting its cost across models.
// Keep token attribution exact and expose the unallocated bill separately;
// never invent a per-model allocation from token ratios or catalog estimates.
func preserveMultiModelProviderTotal(parts []orchestrationUsage, outer map[string]any) []orchestrationUsage {
	total := orchestrationUsage{Model: "unattributed", Native: true}
	if !applyProviderUsageCost(&total, outer) {
		return parts
	}
	known := 0.0
	allProvider := true
	for _, part := range parts {
		if part.CostKnown && part.CostSource == "provider" {
			known += part.EstimatedCostUSD
		} else {
			allProvider = false
		}
	}
	if allProvider {
		return parts
	}
	for i := range parts {
		if parts[i].CostSource != "provider" || known > total.EstimatedCostUSD+1e-9 {
			parts[i].EstimatedCostUSD = 0
			parts[i].CostKnown = false
			parts[i].CostSource = "provider-unallocated"
			parts[i].PricingModel = ""
		}
	}
	if known <= total.EstimatedCostUSD+1e-9 {
		total.EstimatedCostUSD = max(0, total.EstimatedCostUSD-known)
	}
	return append(parts, total)
}

func unwrapNativeUsage(value map[string]any) map[string]any {
	for _, key := range []string{"usage", "last"} {
		if nested, ok := value[key].(map[string]any); ok {
			value = nested
		}
	}
	return value
}

func nativeUsageCount(value map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if number, ok := value[key].(float64); ok && number >= 0 && number < math.MaxInt64 {
			return int64(number)
		}
		if number, ok := value[key].(json.Number); ok {
			if n, err := number.Int64(); err == nil && n >= 0 {
				return n
			}
		}
	}
	return 0
}

func nativeUsageCounts(model string, value map[string]any) orchestrationUsage {
	usage := orchestrationUsage{Model: strings.TrimSpace(model), Native: true}
	usage.InputTokens = nativeUsageCount(value, "input_tokens", "inputTokens", "prompt_tokens", "promptTokens")
	usage.OutputTokens = nativeUsageCount(value, "output_tokens", "outputTokens", "completion_tokens", "completionTokens")
	usage.CacheReadTokens = nativeUsageCount(value, "cached_input_tokens", "cache_read_input_tokens", "cachedInputTokens", "cacheReadInputTokens", "cacheReadTokens")
	usage.CacheWriteTokens = nativeUsageCount(value, "cache_write_input_tokens", "cache_creation_input_tokens", "cacheWriteInputTokens", "cacheCreationInputTokens", "cacheWriteTokens")
	if usage.CacheReadTokens == 0 {
		for _, key := range []string{"input_tokens_details", "prompt_tokens_details"} {
			if details, ok := value[key].(map[string]any); ok {
				usage.CacheReadTokens = nativeUsageCount(details, "cached_tokens")
			}
		}
	}
	return usage
}

func applyProviderUsageCost(usage *orchestrationUsage, value map[string]any) bool {
	for _, key := range []string{"total_cost_usd", "costUSD", "cost_usd"} {
		if cost, ok := value[key].(float64); ok && cost >= 0 && !math.IsNaN(cost) && !math.IsInf(cost, 0) {
			usage.EstimatedCostUSD, usage.CostKnown, usage.CostSource = cost, true, "provider"
			usage.PricingModel = ""
			return true
		}
	}
	return false
}

func priceNativeUsage(cli string, usage *orchestrationUsage, value map[string]any) {
	if applyProviderUsageCost(usage, value) {
		return
	}
	quote, ok := usagepricing.Estimate(cli, usage.Model, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens)
	if ok {
		usage.EstimatedCostUSD, usage.CostKnown = quote.CostUSD, true
		usage.CostSource, usage.PricingModel = quote.Source, quote.PricingModel
	} else {
		usage.CostSource = "unavailable"
	}
}

// Some Claude-compatible endpoints omit result.modelUsage. Keep the observed
// message models and deduplicate repeated assistant blocks as a fallback.
type claudeUsageObservations struct {
	messages map[string]map[string]any
}

func (o *claudeUsageObservations) observe(msg map[string]any) {
	message, ok := msg["message"].(map[string]any)
	if !ok {
		return
	}
	id, model := firstString(message, "id"), firstString(message, "model")
	usage, ok := message["usage"].(map[string]any)
	if !ok || model == "" {
		return
	}
	if o.messages == nil {
		o.messages = make(map[string]map[string]any)
	}
	if id == "" {
		return
	} // No stable identity: do not guess or duplicate usage.
	key := model + "\x00" + id
	if previous, exists := o.messages[key]; exists {
		for _, field := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
			usage[field] = float64(max(nativeUsageCount(previous, field), nativeUsageCount(usage, field)))
		}
	}
	usage["model"] = model
	o.messages[key] = usage
}

func (o *claudeUsageObservations) payload(result map[string]any) json.RawMessage {
	payload := make(map[string]any)
	for _, key := range []string{"usage", "modelUsage", "costUSD", "total_cost_usd", "model"} {
		if value, ok := result[key]; ok {
			payload[key] = value
		}
	}
	models := make(map[string]any)
	for _, observation := range o.messages {
		model := firstString(observation, "model")
		entry, ok := models[model].(map[string]any)
		if !ok {
			entry = make(map[string]any)
			models[model] = entry
		}
		for _, field := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
			entry[field] = float64(nativeUsageCount(entry, field) + nativeUsageCount(observation, field))
		}
	}
	if len(models) == 1 && firstString(payload, "model") == "" {
		for model := range models {
			payload["model"] = model
		}
	}
	nativeModels, _ := payload["modelUsage"].(map[string]any)
	aggregate := nativeUsageCounts("", unwrapNativeUsage(payload))
	if len(nativeModels) == 0 && len(models) > 0 && (!orchestrationUsageHasCounts(aggregate) || len(models) > 1) {
		payload["modelUsage"] = models
	}
	raw, _ := json.Marshal(payload)
	return raw
}
