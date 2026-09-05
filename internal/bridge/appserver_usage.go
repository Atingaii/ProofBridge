package bridge

import "encoding/json"

// Codex input counts include cached input; keep that native convention here.
// The Hub removes the overlap when projecting disjoint display categories.
type appServerTokenUsage struct {
	Input      int64 `json:"inputTokens"`
	Cached     int64 `json:"cachedInputTokens"`
	CacheWrite int64 `json:"cacheWriteInputTokens"`
	Output     int64 `json:"outputTokens"`
	Reasoning  int64 `json:"reasoningOutputTokens"`
}

type appServerUsageAccumulator struct {
	previous *appServerTokenUsage
	sum      appServerTokenUsage
	observed bool
}

func (a *appServerUsageAccumulator) observe(payload any) json.RawMessage {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var update struct {
		Total *appServerTokenUsage `json:"total"`
		Last  *appServerTokenUsage `json:"last"`
	}
	if json.Unmarshal(raw, &update) != nil || update.Last == nil {
		return nil
	}
	delta := *update.Last
	if update.Total != nil && a.previous != nil {
		if *update.Total == *a.previous {
			return nil // Repeated cumulative notification, not another call.
		}
		current, previous := update.Total, a.previous
		if current.Input >= previous.Input && current.Cached >= previous.Cached &&
			current.CacheWrite >= previous.CacheWrite && current.Output >= previous.Output && current.Reasoning >= previous.Reasoning {
			delta = appServerTokenUsage{current.Input - previous.Input, current.Cached - previous.Cached,
				current.CacheWrite - previous.CacheWrite, current.Output - previous.Output, current.Reasoning - previous.Reasoning}
		}
	}
	// The first total may include a resumed thread's prior turns. Only its
	// last response belongs to this turn. After a counter reset, do the same.
	a.previous = update.Total
	a.sum.Input += delta.Input
	a.sum.Cached += delta.Cached
	a.sum.CacheWrite += delta.CacheWrite
	a.sum.Output += delta.Output
	a.sum.Reasoning += delta.Reasoning
	a.observed = true
	result, _ := json.Marshal(a.sum)
	return result
}
