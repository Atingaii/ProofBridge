package usagepricing

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tencent/codex-bridge/internal/modelcatalog"
)

func TestNewModelPricingFromRefreshedCatalog(t *testing.T) {
	previous := modelcatalog.Default
	modelcatalog.Default = &modelcatalog.Catalog{}
	defer func() { modelcatalog.Default = previous }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"deepseek":{"models":{"deepseek-new":{"cost":{"input":0.15,"output":0.6,"cache_read":0.003}}}}}`))
	}))
	defer srv.Close()
	if err := modelcatalog.Default.Refresh(context.Background(), srv.Client(), srv.URL, ""); err != nil {
		t.Fatal(err)
	}
	quote, ok := Estimate("codex", "deepseek-new", 1000000, 1000000, 500000, 0)
	if !ok || math.Abs(quote.CostUSD-.6765) > 1e-10 || quote.Source != "models.dev" {
		t.Fatalf("wrong normalized price: %+v", quote)
	}
	ledger, ok := EstimateNormalized("codex", "deepseek-new", 500000, 1000000, 500000, 0)
	if !ok || ledger.CostUSD != quote.CostUSD {
		t.Fatal(ledger)
	}
	if _, ok := Estimate("codex", "deepseek-new", 100, 100, 0, 10); ok {
		t.Fatal("invented cache write price")
	}
}

func TestCatalogContextTierAndExplicitFreeCache(t *testing.T) {
	previous := modelcatalog.Default
	modelcatalog.Default = &modelcatalog.Catalog{}
	defer func() { modelcatalog.Default = previous }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"openai":{"models":{"tiered-model":{"cost":{"input":1,"output":2,"cache_read":0,"cache_write":0,"tiers":[{"input":2,"output":3,"cache_read":0,"cache_write":0,"tier":{"type":"context","size":100}}]}}}}}`))
	}))
	defer srv.Close()
	if err := modelcatalog.Default.Refresh(context.Background(), srv.Client(), srv.URL, ""); err != nil {
		t.Fatal(err)
	}
	quote, ok := EstimateNormalized("codex", "tiered-model", 60, 10, 50, 5)
	if !ok || math.Abs(quote.CostUSD-.00015) > 1e-12 {
		t.Fatal(quote, ok)
	}
	snapshot, ok := Estimate("codex", "tiered-model", 110, 10, 50, 5)
	if !ok || snapshot.CostUSD != quote.CostUSD {
		t.Fatal(snapshot, ok)
	}
}
