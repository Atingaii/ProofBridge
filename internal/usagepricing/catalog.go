package usagepricing

import (
	"github.com/tencent/codex-bridge/internal/modelcatalog"
	"strings"
)

const SourceOfficialCatalog = "official-catalog"

type Rates struct {
	Source               string
	MissingCacheRead     bool
	MissingCacheWrite    bool
	Model                string
	InputPerMillion      float64
	CachedPerMillion     float64
	CacheWritePerMillion float64
	OutputPerMillion     float64
	CachedIncludedInput  bool
}

type Quote struct {
	PricingModel string
	CostUSD      float64
	Source       string
}

// Estimate applies the standard public API price for an explicitly known model.
// Unresolved aliases retain token counts without inventing a billing model.
func Estimate(cli, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) (Quote, bool) {
	rates, ok := resolveForUsage(cli, model, inputTokens, cacheReadTokens, false)
	if !ok || inputTokens+outputTokens+cacheReadTokens+cacheWriteTokens <= 0 {
		return Quote{}, false
	}
	if cacheReadTokens > 0 && rates.MissingCacheRead || cacheWriteTokens > 0 && (rates.MissingCacheWrite || rates.Source == "" && rates.CacheWritePerMillion == 0) {
		return Quote{}, false
	}
	billableInput := inputTokens
	if rates.CachedIncludedInput {
		billableInput -= cacheReadTokens
		if billableInput < 0 {
			billableInput = 0
		}
	}
	cost := float64(billableInput)*rates.InputPerMillion/1e6 +
		float64(cacheReadTokens)*rates.CachedPerMillion/1e6 +
		float64(cacheWriteTokens)*rates.CacheWritePerMillion/1e6 +
		float64(outputTokens)*rates.OutputPerMillion/1e6
	source := "catalog"
	if strings.HasPrefix(rates.Model, "gpt-") {
		source = SourceOfficialCatalog
	}
	if rates.Source != "" {
		source = rates.Source
	}
	return Quote{PricingModel: rates.Model, CostUSD: cost, Source: source}, true
}

// EstimateNormalized prices a ledger event whose inputTokens already excludes
// cache reads. This avoids subtracting cached input a second time.
func EstimateNormalized(cli, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) (Quote, bool) {
	rates, ok := resolveForUsage(cli, model, inputTokens, cacheReadTokens, true)
	if !ok || inputTokens+outputTokens+cacheReadTokens+cacheWriteTokens <= 0 {
		return Quote{}, false
	}
	if cacheReadTokens > 0 && rates.MissingCacheRead || cacheWriteTokens > 0 && (rates.MissingCacheWrite || rates.Source == "" && rates.CacheWritePerMillion == 0) {
		return Quote{}, false
	}
	cost := float64(inputTokens)*rates.InputPerMillion/1e6 + float64(cacheReadTokens)*rates.CachedPerMillion/1e6 + float64(cacheWriteTokens)*rates.CacheWritePerMillion/1e6 + float64(outputTokens)*rates.OutputPerMillion/1e6
	source := "catalog"
	if strings.HasPrefix(rates.Model, "gpt-") {
		source = SourceOfficialCatalog
	}
	if rates.Source != "" {
		source = rates.Source
	}
	return Quote{PricingModel: rates.Model, CostUSD: cost, Source: source}, true
}

func Resolve(cli, model string) (Rates, bool) {
	cli = strings.ToLower(strings.TrimSpace(cli))
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || model == "default" || model == "codex" {
		return Rates{}, false
	}
	if m, ok := modelcatalog.Default.Lookup(model); ok {
		return dynamicRates(cli, m.ID, m.Cost)
	}

	switch {
	case strings.Contains(model, "gpt-5.6-sol"):
		return openAI(model, "gpt-5.6-sol", 5, 0.5, 6.25, 30), true
	case strings.Contains(model, "gpt-5.6-terra"):
		return openAI(model, "gpt-5.6-terra", 2, 0.2, 2.5, 12), true
	case strings.Contains(model, "gpt-5.6-luna"):
		return openAI(model, "gpt-5.6-luna", 0.2, 0.02, 0.25, 1.2), true
	case strings.Contains(model, "gpt-5.5-pro"):
		return openAI(model, "gpt-5.5-pro", 30, 0, 0, 180), true
	case strings.Contains(model, "gpt-5.5"):
		return openAI(model, "gpt-5.5", 5, 0.5, 0, 30), true
	case strings.Contains(model, "gpt-5.4-mini"):
		return openAI(model, "gpt-5.4-mini", 0.75, 0.075, 0, 4.5), true
	case strings.Contains(model, "gpt-5.4-nano"):
		return openAI(model, "gpt-5.4-nano", 0.2, 0.02, 0, 1.25), true
	case strings.Contains(model, "gpt-5.4-pro"):
		return openAI(model, "gpt-5.4-pro", 30, 0, 0, 180), true
	case strings.Contains(model, "gpt-5.4"):
		return openAI(model, "gpt-5.4", 2.5, 0.25, 0, 15), true
	case strings.Contains(model, "gpt-5.3-codex"):
		return openAI(model, "gpt-5.3-codex", 1.75, 0.175, 0, 14), true
	case strings.Contains(model, "gpt-5.2-pro"):
		return openAI(model, "gpt-5.2-pro", 21, 0, 0, 168), true
	case strings.Contains(model, "gpt-5.2"):
		return openAI(model, "gpt-5.2", 1.75, 0.175, 0, 14), true
	case strings.Contains(model, "gpt-5-mini"):
		return openAI(model, "gpt-5-mini", 0.25, 0.025, 0, 2), true
	case strings.Contains(model, "gpt-5-nano"):
		return openAI(model, "gpt-5-nano", 0.05, 0.005, 0, 0.4), true
	case strings.Contains(model, "gpt-5-pro"):
		return openAI(model, "gpt-5-pro", 15, 0, 0, 120), true
	case strings.Contains(model, "gpt-5.1"):
		return openAI(model, "gpt-5.1", 1.25, 0.125, 0, 10), true
	case model == "gpt-5" || strings.HasPrefix(model, "gpt-5-202"):
		return openAI(model, "gpt-5", 1.25, 0.125, 0, 10), true
	case strings.Contains(model, "gpt-4o-mini"):
		return openAI(model, "gpt-4o-mini", 0.15, 0.075, 0, 0.6), true
	case model == "gpt-4o" || strings.HasPrefix(model, "gpt-4o-2024"):
		return openAI(model, "gpt-4o", 2.5, 1.25, 0, 10), true
	case claudeVersion(model, "opus", "4-5", "4-6", "4-7", "4-8", "5"):
		return claudeRates(model, 5, 25), true
	case claudeVersion(model, "opus", "4", "4-1"):
		return claudeRates(model, 15, 75), true
	case claudeVersion(model, "sonnet", "5"):
		return claudeRates(model, 2, 10), true
	case claudeVersion(model, "sonnet", "4", "4-5", "4-6"):
		return claudeRates(model, 3, 15), true
	case claudeVersion(model, "haiku", "4-5"):
		return claudeRates(model, 1, 5), true
	case claudeVersion(model, "haiku", "3-5") || strings.HasPrefix(model, "claude-3-5-haiku-"):
		return claudeRates(model, 0.8, 4), true
	default:
		return Rates{}, false
	}
}

func openAI(actualModel, fallbackModel string, input, cached, cacheWrite, output float64) Rates {
	return Rates{
		Model:                normalizedModel(actualModel, fallbackModel),
		InputPerMillion:      input,
		CachedPerMillion:     cached,
		CacheWritePerMillion: cacheWrite,
		OutputPerMillion:     output,
		CachedIncludedInput:  true,
	}
}

func normalizedModel(model, fallback string) string {
	if strings.TrimSpace(model) == "" {
		return fallback
	}
	return model
}

// Match an exact published version or dated snapshot. Do not price an unknown
// future Opus/Haiku release using a legacy family-wide rate.
func claudeVersion(model, family string, versions ...string) bool {
	model = strings.ReplaceAll(model, ".", "-")
	for _, version := range versions {
		base := "claude-" + family + "-" + version
		if model == base {
			return true
		}
		suffix := strings.TrimPrefix(model, base+"-")
		if suffix == model || len(suffix) != 8 {
			continue
		}
		digits := true
		for _, char := range suffix {
			if char < '0' || char > '9' {
				digits = false
			}
		}
		if digits {
			return true
		}
	}
	return false
}

// Standard five-minute cache write pricing; provider amounts take precedence.
// Source: https://platform.claude.com/docs/en/about-claude/pricing (2026-09-08).
func claudeRates(model string, input, output float64) Rates {
	return Rates{Model: model, InputPerMillion: input, CachedPerMillion: input * .1, CacheWritePerMillion: input * 1.25, OutputPerMillion: output}
}

func dynamicRates(cli, id string, cost modelcatalog.Cost) (Rates, bool) {
	if cost.Input == nil || cost.Output == nil {
		return Rates{}, false
	}
	rates := Rates{Model: id, Source: "models.dev", InputPerMillion: *cost.Input, OutputPerMillion: *cost.Output, CachedIncludedInput: strings.EqualFold(cli, "codex"), MissingCacheRead: cost.CacheRead == nil, MissingCacheWrite: cost.CacheWrite == nil}
	if cost.CacheRead != nil {
		rates.CachedPerMillion = *cost.CacheRead
	}
	if cost.CacheWrite != nil {
		rates.CacheWritePerMillion = *cost.CacheWrite
	}
	return rates, true
}
func resolveForUsage(cli, model string, input, cached int64, normalized bool) (Rates, bool) {
	if m, ok := modelcatalog.Default.Lookup(model); ok {
		context := input
		if normalized || !strings.EqualFold(cli, "codex") {
			context += cached
		}
		return dynamicRates(cli, m.ID, m.CostForContext(context))
	}
	return Resolve(cli, model)
}
