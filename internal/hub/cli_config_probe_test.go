package hub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestHubProbeInferenceRejectsWebsiteAndJSONErrors(t *testing.T) {
	for _, body := range []string{"<html>New API</html>", `{"error":{"message":"private-key"}}`, `{}`, ""} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		status, err := hubProbeInference(context.Background(), s.Client(), s.URL, "model", "test-key", "codex")
		s.Close()
		if status != 200 || err == nil {
			t.Fatalf("body %q: status=%d err=%v", body, status, err)
		}
		if strings.Contains(err.Error(), "private-key") {
			t.Fatal("provider response leaked")
		}
	}
}

func TestCLIConfigProbeRejectsNonPublicDestinations(t *testing.T) {
	for _, host := range []string{"localhost", "api.localhost", "127.0.0.1", "10.0.0.1", "169.254.169.254", "[::1]", "[fc00::1]"} {
		if err := validateCLIConfigHost(host); err == nil {
			t.Fatalf("validateCLIConfigHost(%q) accepted a non-public destination", host)
		}
	}
	for _, raw := range []string{"100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1"} {
		ip := netip.MustParseAddr(raw)
		if isPublicCLIConfigIP(ip) {
			t.Fatalf("isPublicCLIConfigIP(%q) = true", raw)
		}
	}
	if !isPublicCLIConfigIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public address rejected")
	}
}

func TestCLIConfigProviderCandidatesRejectHTTPOutsideDevelopment(t *testing.T) {
	s, _ := newAuthTestServer(t)
	s.cfg.App.Env = "prod"
	if _, err := s.cliConfigProviderCandidates("http://api.example.com/v1"); err == nil {
		t.Fatal("production candidate generation accepted HTTP")
	}
	if _, err := s.cliConfigProviderCandidates("https://127.0.0.1/v1"); err == nil {
		t.Fatal("candidate generation accepted loopback")
	}
}
