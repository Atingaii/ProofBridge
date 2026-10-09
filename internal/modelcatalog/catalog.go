// Package modelcatalog supplies optional, public model metadata. No request to
// a provider or catalog is made on a statistics/configuration request path.
package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const URL = "https://models.dev/api.json"
const maxBytes = 32 << 20

type Cost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
	Tiers      []struct {
		Input      *float64 `json:"input"`
		Output     *float64 `json:"output"`
		CacheRead  *float64 `json:"cache_read"`
		CacheWrite *float64 `json:"cache_write"`
		Tier       struct {
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tier"`
	} `json:"tiers"`
}

func (m Model) CostForContext(tokens int64) Cost {
	cost := m.Cost
	threshold := int64(0)
	for _, tier := range m.Cost.Tiers {
		if tier.Tier.Type == "context" && tier.Tier.Size > 0 && tokens > tier.Tier.Size && tier.Tier.Size > threshold {
			threshold = tier.Tier.Size
			cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite = tier.Input, tier.Output, tier.CacheRead, tier.CacheWrite
		}
	}
	return cost
}

type Model struct {
	ID    string `json:"id"`
	Cost  Cost   `json:"cost"`
	Limit struct {
		Context int `json:"context"`
	} `json:"limit"`
	ReasoningOptions []struct {
		Type   string   `json:"type"`
		Values []string `json:"values"`
	} `json:"reasoning_options"`
	Provider string `json:"-"`
}
type snapshot struct {
	FetchedAt int64           `json:"fetchedAt"`
	Data      json.RawMessage `json:"data"`
}
type Catalog struct {
	mu        sync.RWMutex
	models    map[string]Model
	fetchedAt int64
}

var Default = &Catalog{}

func normalize(id string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "models/")
}
func (c *Catalog) Lookup(id string) (Model, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.models[normalize(id)]
	return m, ok
}
func (c *Catalog) UpdatedAt() int64 { c.mu.RLock(); defer c.mu.RUnlock(); return c.fetchedAt }

// Ignore reseller entries: they can assign incompatible prices/capabilities to
// the same ID. Bare IDs are admitted only when unambiguous among originators.
func (c *Catalog) install(raw []byte, at int64) error {
	var providers map[string]struct {
		Models map[string]Model `json:"models"`
	}
	if err := json.Unmarshal(raw, &providers); err != nil {
		return err
	}
	models := map[string]Model{}
	bare := map[string]Model{}
	ambiguous := map[string]bool{}
	for _, provider := range []string{"openai", "anthropic", "deepseek", "google", "xai", "moonshotai", "zhipuai", "alibaba", "mistral", "minimax", "cohere", "perplexity", "meta"} {
		for id, m := range providers[provider].Models {
			id = normalize(id)
			if id == "" || m.ID != "" && normalize(m.ID) != id {
				continue
			}
			m.ID, m.Provider = id, provider
			valid := true
			for _, rate := range []*float64{m.Cost.Input, m.Cost.Output, m.Cost.CacheRead, m.Cost.CacheWrite} {
				if rate != nil && (*rate < 0 || math.IsInf(*rate, 0) || math.IsNaN(*rate)) {
					valid = false
				}
			}
			for _, tier := range m.Cost.Tiers {
				for _, rate := range []*float64{tier.Input, tier.Output, tier.CacheRead, tier.CacheWrite} {
					if rate != nil && (*rate < 0 || math.IsInf(*rate, 0) || math.IsNaN(*rate)) {
						valid = false
					}
				}
			}
			if !valid {
				return errors.New("invalid catalog rate")
			}
			models[provider+"/"+id] = m
			if _, exists := bare[id]; exists {
				ambiguous[id] = true
			}
			bare[id] = m
		}
	}
	for id, m := range bare {
		if !ambiguous[id] {
			models[id] = m
		}
	}
	if len(models) == 0 {
		return errors.New("empty model catalog")
	}
	c.mu.Lock()
	c.models, c.fetchedAt = models, at
	c.mu.Unlock()
	return nil
}

func (c *Catalog) Refresh(ctx context.Context, client *http.Client, url, cachePath string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ProofBridge-model-catalog/1")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("catalog returned non-200 status")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxBytes {
		return errors.New("model catalog exceeds size limit")
	}
	at := time.Now().Unix()
	if err := c.install(raw, at); err != nil {
		return err
	}
	if cachePath == "" {
		return nil
	}
	encoded, err := json.Marshal(snapshot{at, raw})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(cachePath), ".model-catalog-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(encoded); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), cachePath)
}

func (c *Catalog) Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var s snapshot
	if err := json.NewDecoder(io.LimitReader(f, maxBytes+1024)).Decode(&s); err != nil {
		return err
	}
	if s.FetchedAt <= 0 {
		return errors.New("invalid catalog timestamp")
	}
	return c.install(s.Data, s.FetchedAt)
}

func (c *Catalog) Run(ctx context.Context, cachePath string) {
	client := &http.Client{Timeout: 20 * time.Second}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		if err := c.Refresh(ctx, client, URL, cachePath); err != nil && ctx.Err() == nil {
			slog.Warn("[hub] model catalog refresh failed; retaining last catalog", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m Model) Efforts(cli string) []string {
	allowed := map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	if cli == "codex" {
		allowed["none"] = true
		allowed["minimal"] = true
	}
	var levels []string
	for _, option := range m.ReasoningOptions {
		if option.Type == "effort" {
			for _, level := range option.Values {
				if allowed[level] {
					levels = append(levels, level)
					delete(allowed, level)
				}
			}
		}
	}
	return levels
}
