package hub

import (
	"context"
	"errors"
	"testing"

	"github.com/tencent/codex-bridge/internal/store"
)

func TestAutomationRejectsUnsupportedVerificationWithoutDispatch(t *testing.T) {
	t.Parallel()
	s, st, uid, agentID := newOrchestrationTestServer(t)
	bridge := automationRunTestBridge(s, agentID)
	defer s.pool.UnregisterAgent(agentID, bridge)
	ctx := context.Background()
	request := automationRaw(t, map[string]any{
		"clientRequestId": "unsupported-authority", "prompt": "verify the proof", "endpointId": agentID,
		"workerPair": "codex-codex", "profile": "formal-proof", "verificationMode": "retired-verifier",
	})
	for name, invoke := range map[string]func() error{
		"preview": func() error { _, err := s.previewAutomationRun(ctx, uid, request); return err },
		"start":   func() error { _, err := s.startAutomationRun(ctx, uid, request, ""); return err },
	} {
		err := invoke()
		var operation *operationError
		if !errors.As(err, &operation) || operation.Code != "BAD_VERIFICATION_MODE" {
			t.Fatalf("%s unsupported authority = %v", name, err)
		}
	}
	page, err := st.ListOrchestrationHistory(ctx, uid, store.OrchestrationHistoryFilter{})
	if err != nil || len(page.Runs) != 0 {
		t.Fatalf("unsupported authority created run: %#v, %v", page, err)
	}
	select {
	case frame := <-bridge.send:
		t.Fatalf("unsupported authority dispatched %s", frame.Type)
	default:
	}
}
