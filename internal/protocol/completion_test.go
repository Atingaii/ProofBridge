package protocol

import (
	"encoding/json"
	"testing"
)

func TestCompletionEvidenceCompatibility(t *testing.T) {
	p := OrchestrationEventPayload{RunID: "run", Kind: "run.end", RunConclusion: &RunConclusion{Outcome: "completed", VerificationStatus: "continue"}, RunEndData: &RunEndData{TerminalReason: "stalled", CompletionObservation: &CompletionObservation{Round: 6, TaskDigest: "original-task"}, VerifierVerdict: &VerifierVerdict{Status: "continue", Checkers: []VerifierCheck{{Name: "verification", Status: "continue", Code: "proof-incomplete", Target: "Sort.sort_correct", Evidence: "sort_correct is an assumption"}}}}}
	decoded, err := Decode[OrchestrationEventPayload](MustEnvelope(TypeOrchestrationEvent, "", p))
	if err != nil || decoded.RunConclusion.VerificationStatus != "continue" || decoded.RunEndData.CompletionObservation.TaskDigest != "original-task" || decoded.RunEndData.VerifierVerdict.Checkers[0] != p.RunEndData.VerifierVerdict.Checkers[0] {
		t.Fatalf("lost independent completion evidence: %#v %v", decoded, err)
	}
	var legacy VerifierCheck
	if err := json.Unmarshal([]byte(`{"name":"verification","status":"continue","reason":"unproved"}`), &legacy); err != nil || legacy.Code != "" || legacy.Target != "" || legacy.Evidence != "" {
		t.Fatalf("legacy checker rejected: %#v %v", legacy, err)
	}
}
