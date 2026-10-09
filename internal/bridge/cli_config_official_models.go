package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
)

const officialModelPageLimit = 100
const officialModelPageBytes = 1024 * 1024
const officialModelPageCount = 10

func officialCodexProviderArgs() []string {
	return []string{"-c", "model_provider=\"openai\"", "-c", "forced_login_method=\"chatgpt\""}
}

type nativeOfficialModel struct {
	Model                     string `json:"model"`
	DisplayName               string `json:"displayName"`
	Hidden                    bool   `json:"hidden"`
	IsDefault                 bool   `json:"isDefault"`
	DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

// officialCodexModels uses the target machine's native directory, not the
// provider catalog. It never starts a thread/turn or writes config/auth files.
func (m *cliConfigManager) officialCodexModels(parent context.Context) ([]protocol.OfficialCLIModelMetadata, error) {
	ctx, cancel := context.WithTimeout(parent, cliConfigCallLimit)
	defer cancel()
	runner := NewCodexAppServerRunner(m.cfg)
	client, err := runner.startWithArgs(ctx, RunnerRequest{}, officialCodexProviderArgs())
	if err != nil {
		return nil, fmt.Errorf("start official Codex directory: %w", err)
	}
	defer client.closeWithTimeout(appServerPrepareCloseTimeout)
	// Drain notifications so native initialization cannot block the RPC reader.
	go func() {
		for range client.events {
		}
	}()
	if _, err := client.request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "codex-bridge", "title": "ProofBridge", "version": "dev"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return nil, fmt.Errorf("initialize official Codex directory: %w", err)
	}
	return readOfficialCodexModelPages(ctx, client.request)
}

func readOfficialCodexModelPages(ctx context.Context, request func(context.Context, string, any) (json.RawMessage, error)) ([]protocol.OfficialCLIModelMetadata, error) {
	models := make([]protocol.OfficialCLIModelMetadata, 0)
	seenModels := make(map[string]bool)
	seenCursors := make(map[string]bool)
	cursor := ""
	total := 0
	for pageIndex := 0; pageIndex < officialModelPageCount; pageIndex++ {
		params := map[string]any{"limit": officialModelPageLimit, "includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := request(ctx, "model/list", params)
		if err != nil {
			return nil, fmt.Errorf("list official Codex models: %w", err)
		}
		if len(raw) > officialModelPageBytes {
			return nil, errors.New("official model directory page exceeds size limit")
		}
		var page struct {
			Data       []nativeOfficialModel `json:"data"`
			NextCursor string                `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("decode official model directory: %w", err)
		}
		total += len(page.Data)
		if total > protocol.MaxOfficialModels {
			return nil, errors.New("official model directory exceeds model limit")
		}
		for _, native := range page.Data {
			if native.Hidden {
				continue
			}
			model := strings.TrimSpace(native.Model)
			if model == "" || len(model) > protocol.MaxOfficialModelIDBytes || seenModels[model] {
				return nil, errors.New("official model directory contains invalid or duplicate model")
			}
			seenModels[model] = true
			display := strings.TrimSpace(native.DisplayName)
			if len(display) > protocol.MaxOfficialModelDisplayNameBytes || len(native.SupportedReasoningEfforts) > protocol.MaxOfficialReasoningEfforts {
				return nil, errors.New("official model metadata exceeds size limit")
			}
			levels := make([]string, 0, len(native.SupportedReasoningEfforts))
			for _, item := range native.SupportedReasoningEfforts {
				effort := strings.TrimSpace(item.ReasoningEffort)
				if effort == "" || len(effort) > protocol.MaxOfficialReasoningEffortBytes || workerProfileContains(levels, effort) {
					return nil, errors.New("official model directory contains invalid reasoning effort")
				}
				levels = append(levels, effort)
			}
			defaultEffort := strings.TrimSpace(native.DefaultReasoningEffort)
			if defaultEffort != "" && !workerProfileContains(levels, defaultEffort) {
				return nil, errors.New("official model default reasoning effort is unsupported")
			}
			models = append(models, protocol.OfficialCLIModelMetadata{
				Model: model, DisplayName: display, SupportedReasoningEfforts: levels,
				DefaultReasoningEffort: defaultEffort, IsDefault: native.IsDefault,
			})
		}
		cursor = strings.TrimSpace(page.NextCursor)
		if cursor == "" {
			if len(models) == 0 {
				return nil, errors.New("official model directory is empty")
			}
			return models, nil
		}
		if len(cursor) > 4096 || seenCursors[cursor] {
			return nil, errors.New("official model directory contains invalid pagination cursor")
		}
		seenCursors[cursor] = true
	}
	return nil, errors.New("official model directory exceeds pagination limit")
}
