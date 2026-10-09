package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRefreshNewModelCacheAndFailure(t *testing.T) {
	body := `{"deepseek":{"models":{"new-model":{"id":"new-model","cost":{"input":0.15,"output":0.6,"cache_read":0.003},"limit":{"context":1000000},"reasoning_options":[{"type":"effort","values":["low","high","invalid","max"]}]}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credential sent")
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := &Catalog{}
	cache := filepath.Join(t.TempDir(), "catalog.json")
	if err := c.Refresh(context.Background(), srv.Client(), srv.URL, cache); err != nil {
		t.Fatal(err)
	}
	m, ok := c.Lookup("deepseek/new-model")
	if !ok || m.Limit.Context != 1000000 || *m.Cost.Input != .15 {
		t.Fatalf("missing new model: %+v", m)
	}
	if !reflect.DeepEqual(m.Efforts("codex"), []string{"low", "high", "max"}) {
		t.Fatal(m.Efforts("codex"))
	}
	cached := &Catalog{}
	if err := cached.Load(cache); err != nil {
		t.Fatal(err)
	}
	if _, ok := cached.Lookup("new-model"); !ok {
		t.Fatal("cache missed model")
	}
	body = `{"deepseek":{"models":{"new-model":{"cost":{"input":0.2,"output":0.7}}}}}`
	if err := c.Refresh(context.Background(), srv.Client(), srv.URL, cache); err != nil {
		t.Fatal(err)
	}
	m, _ = c.Lookup("new-model")
	if *m.Cost.Input != .2 {
		t.Fatal("price did not refresh")
	}
	for _, invalid := range []string{`bad json`, `{}`, `{"deepseek":{"models":{"new-model":{"cost":{"input":-1}}}}}`} {
		body = invalid
		if err := c.Refresh(context.Background(), srv.Client(), srv.URL, cache); err == nil {
			t.Fatal("accepted invalid catalog")
		}
		m, _ = c.Lookup("new-model")
		if *m.Cost.Input != .2 {
			t.Fatal("last good data lost")
		}
	}
}

func TestAmbiguousModelsAndResellers(t *testing.T) {
	c := &Catalog{}
	err := c.install([]byte(`{"openai":{"models":{"same":{"cost":{"input":1}}}},"anthropic":{"models":{"same":{"cost":{"input":2}}}},"relay":{"models":{"invented":{"cost":{"input":3}}}}}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"same", "invented", "not-same"} {
		if _, ok := c.Lookup(id); ok {
			t.Fatalf("resolved ambiguous/untrusted %q", id)
		}
	}
	if _, ok := c.Lookup("openai/same"); !ok {
		t.Fatal("qualified model lost")
	}
}
