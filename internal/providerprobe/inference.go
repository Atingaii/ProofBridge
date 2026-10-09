// Package providerprobe validates responses shared by Hub and Bridge probes.
package providerprobe

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const maxResponseBytes = 2 << 20

// ValidateInferenceResponse consumes a bounded non-streaming response body.
// Callers still check HTTP status and own closing the body. Error messages
// deliberately omit upstream bodies, which can contain credentials.
func ValidateInferenceResponse(resp *http.Response, cli string) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("could not read inference response")
	}
	if len(raw) > maxResponseBytes {
		return errors.New("inference response exceeds size limit")
	}
	raw = bytes.TrimSpace(raw)
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") || bytes.HasPrefix(raw, []byte("<")) {
		return errors.New("inference endpoint returned HTML instead of an API response; check Base URL (including /v1)")
	}
	var body struct {
		ID      string          `json:"id"`
		Object  string          `json:"object"`
		Type    string          `json:"type"`
		Status  string          `json:"status"`
		Error   json.RawMessage `json:"error"`
		Output  json.RawMessage `json:"output"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return errors.New("inference endpoint returned invalid or empty JSON")
	}
	if len(body.Error) > 0 && string(body.Error) != "null" {
		return errors.New("inference endpoint returned an API error despite HTTP success")
	}
	if strings.TrimSpace(body.ID) == "" {
		return errors.New("inference endpoint returned unrelated JSON instead of a model response")
	}
	output := body.Output
	if cli == "claude" {
		if body.Type != "message" {
			return errors.New("inference endpoint did not return a Messages API response")
		}
		output = body.Content
	} else {
		if body.Object != "response" {
			return errors.New("inference endpoint did not return a Responses API response")
		}
		// A tiny probe output budget may legitimately produce incomplete output.
		if body.Status != "completed" && body.Status != "incomplete" {
			return errors.New("inference response did not finish successfully")
		}
	}
	var items []json.RawMessage
	if len(output) == 0 || string(output) == "null" || json.Unmarshal(output, &items) != nil {
		return errors.New("inference response is missing its output array")
	}
	return nil
}
