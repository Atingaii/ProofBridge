package protocol

import "testing"

func TestNormalizeVerificationModePreservesUnsupportedAuthority(t *testing.T) {
	for value, want := range map[string]string{
		"": "agent", "  ": "agent", " AGENT ": "agent", "retired-verifier": "retired-verifier",
	} {
		if got := NormalizeVerificationMode(value); got != want {
			t.Fatalf("NormalizeVerificationMode(%q) = %q, want %q", value, got, want)
		}
	}
}
