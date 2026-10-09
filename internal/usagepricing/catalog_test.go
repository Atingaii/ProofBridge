package usagepricing

import "testing"

func TestEstimateCodexDefaultDoesNotInventModel(t *testing.T) {
	if quote, ok := Estimate("codex", "default", 23858, 1605, 0, 0); ok {
		t.Fatalf("unknown default received price: %#v", quote)
	}
}

func TestEstimateOpenAICachedInputIsNotDoubleCharged(t *testing.T) {
	quote, ok := Estimate("codex", "gpt-5.6-sol", 100, 20, 40, 0)
	if !ok || quote.CostUSD < 0.000919 || quote.CostUSD > 0.000921 {
		t.Fatalf("quote = %#v ok=%v", quote, ok)
	}
}

func TestEstimateUnknownModelIsUnavailable(t *testing.T) {
	if quote, ok := Estimate("other", "default", 100, 20, 0, 0); ok {
		t.Fatalf("unexpected quote = %#v", quote)
	}
}

func TestClaudeVersionSpecificRates(t *testing.T) {
	for _, tc := range []struct {
		model         string
		input, output float64
	}{
		{"claude-opus-4-6", 5, 25}, {"claude-opus-4-5-20251101", 5, 25},
		{"claude-opus-4-1", 15, 75}, {"claude-sonnet-4-6", 3, 15},
		{"claude-haiku-4-5", 1, 5}, {"claude-3-5-haiku-20241022", .8, 4},
	} {
		rates, ok := Resolve("claude", tc.model)
		if !ok || rates.InputPerMillion != tc.input || rates.OutputPerMillion != tc.output {
			t.Fatalf("%s: %#v", tc.model, rates)
		}
	}
	for _, model := range []string{"claude-opus-99", "opus", "haiku", "default"} {
		if rates, ok := Resolve("claude", model); ok {
			t.Fatalf("unknown model %s priced: %#v", model, rates)
		}
	}
}
