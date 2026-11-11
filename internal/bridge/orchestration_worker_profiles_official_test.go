package bridge

import (
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestWorkerProfileBindingAcceptsOfficialLoginWithoutPresetID(t *testing.T) {
	payload := protocol.OrchestrationStartPayload{WorkerProfiles: map[string]protocol.WorkerProfileBinding{
		"codex-a": {CLI: "codex", OfficialLogin: true},
	}}
	binding, ok := workerProfileBinding(payload, "codex-a", "codex")
	if !ok || !binding.OfficialLogin {
		t.Fatalf("official login binding = %#v, ok=%v", binding, ok)
	}
}

func TestWorkerProfileBindingRejectsEmptyNonOfficialPreset(t *testing.T) {
	payload := protocol.OrchestrationStartPayload{WorkerProfiles: map[string]protocol.WorkerProfileBinding{
		"codex-a": {CLI: "codex"},
	}}
	if _, ok := workerProfileBinding(payload, "codex-a", "codex"); ok {
		t.Fatal("empty non-official preset binding should be rejected")
	}
}
