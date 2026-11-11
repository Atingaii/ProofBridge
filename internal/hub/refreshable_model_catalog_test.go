package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tencent/codex-bridge/internal/modelcatalog"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestNewModelRefreshesExistingPresetCapabilities(t *testing.T) {
	previous := modelcatalog.Default
	modelcatalog.Default = &modelcatalog.Catalog{}
	defer func() { modelcatalog.Default = previous }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"deepseek":{"models":{"deepseek-new":{"limit":{"context":1000000},"reasoning_options":[{"type":"effort","values":["low","high","max"]}]}}}}`))
	}))
	defer srv.Close()
	if err := modelcatalog.Default.Refresh(context.Background(), srv.Client(), srv.URL, ""); err != nil {
		t.Fatal(err)
	}
	p := store.CLIConfigPreset{CLI: "codex", Model: "deepseek-new", ReasoningEffort: "high"}
	normalizeReviewedPreset(&p)
	if len(p.ReasoningLevels) != 3 || p.ReasoningDefault != "high" || p.ReasoningEffort != "high" {
		t.Fatal(p.ReasoningLevels, p.ReasoningDefault, p.ReasoningEffort)
	}
	profile, ok := reviewedClaudeContextForModel("deepseek-new")
	if !ok || profile.Window != 1000000 {
		t.Fatal(profile)
	}
	usage := orchestrationUsageStats{CLI: "codex", Model: "deepseek-new", CostKnown: true, CostSource: "provider", EstimatedCostUSD: 1.23}
	usage.applyOfficialCatalogFallback()
	if usage.EstimatedCostUSD != 1.23 || usage.CostSource != "provider" {
		t.Fatal("provider amount overwritten")
	}
}
