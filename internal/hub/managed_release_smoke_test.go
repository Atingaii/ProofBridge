//go:build release_smoke

package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/store"
)

// This opt-in acceptance builds two real distributable binaries, signs the
// second one with the publisher, and checks that the unchanged bootstrap
// reconnects the same workspace to a real Hub on the new release.
func TestSignedManagedReleaseAcrossExecutables(t *testing.T) {
	dir := t.TempDir()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	publish := filepath.Join(repo, "scripts", "publish-bridge-release.py")
	key := filepath.Join(dir, "private.pem")
	keyCmd := exec.Command(publish, "keygen", "--key", key)
	raw, err := keyCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pin := strings.TrimSpace(string(raw))
	baseline := filepath.Join(dir, "bootstrap")
	candidate := filepath.Join(dir, "candidate")
	for _, b := range []struct {
		seq  int
		path string
	}{{1, baseline}, {2, candidate}} {
		flags := fmt.Sprintf("-s -w -X main.Version=smoke-%d -X main.BridgeReleaseSequence=%d -X main.BridgeReleasePublicKey=%s", b.seq, b.seq, pin)
		cmd := exec.Command(goTool, "build", "-ldflags", flags, "-o", b.path, ".")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %d: %v %s", b.seq, err, out)
		}
	}
	releases := filepath.Join(dir, "downloads", "releases")
	pub := exec.Command(publish, "publish", "--key", key, "--directory", releases, "--platform", runtime.GOOS+"-"+runtime.GOARCH, "--sequence", strconv.Itoa(2), "--version", "smoke-2", "--binary", candidate)
	if out, err := pub.CombinedOutput(); err != nil {
		t.Fatalf("publish: %v %s", err, out)
	}
	// The conventional installer endpoint serves this release even when the
	// Hub and Bridge have separate builds for the same native architecture.
	rec := httptest.NewRecorder()
	serveBridgePlatformBinary(rec, httptest.NewRequest("GET", "/", nil), filepath.Join(dir, "bootstrap-hub"), runtime.GOOS, runtime.GOARCH)
	built, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || sha256.Sum256(rec.Body.Bytes()) != sha256.Sum256(built) {
		t.Fatalf("native install did not use published binary: %d %q", rec.Code, bytes.TrimSpace(rec.Body.Bytes()[:min(120, rec.Body.Len())]))
	}
	s, st, userID, agentID := newOrchestrationTestServer(t)
	token := store.NewToken("enr")
	if err := st.CreateEnrollTokenForUser(context.Background(), token, userID, "signed release smoke", nil); err != nil {
		t.Fatal(err)
	}
	visible := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, ok := strings.CutPrefix(r.URL.Path, "/downloads/releases/"); ok {
			select {
			case <-visible:
			case <-r.Context().Done():
				return
			}
			serveManagedDownload(w, r, releases, name)
			return
		}
		s.ServeHTTP(w, r)
	}))
	defer server.Close()
	reaped := make(chan error, 1)
	logFile, err := os.Create(filepath.Join(dir, "connection.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(baseline, "connect", "--hub", server.URL, "--cwd", dir, "--runner", "echo", "--name", "release-smoke", "--machine-id", "machine", "--machine-id-file", filepath.Join(dir, "machine"), "--", token)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "BRIDGE_CODEX_PATH="+filepath.Join(dir, "unused-codex"), "BRIDGE_CLAUDE_PATH="+filepath.Join(dir, "unused-claude"))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { reaped <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-reaped:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-reaped
		}
	}()
	waitVersion := func(version string) {
		t.Helper()
		end := time.Now().Add(15 * time.Second)
		for time.Now().Before(end) {
			if i, ok := s.pool.AgentConnectionInfo(agentID); ok && i.Version == version {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		// These fixtures contain one local-only, short-lived enrollment value.
		raw, _ := os.ReadFile(logFile.Name())
		t.Fatalf("version %s never registered: %s", version, strings.ReplaceAll(string(raw), token, "[test token]"))
	}
	waitVersion("smoke-1")
	close(visible)
	waitVersion("smoke-2")
	agent, err := st.AgentByMachineID(context.Background(), "machine")
	if err != nil || agent.ID != agentID {
		t.Fatalf("endpoint identity changed: %v", err)
	}
	t.Log("one publication upgraded the same enrolled endpoint from smoke-1 to smoke-2 through its original bootstrap")
}
