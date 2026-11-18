package hub

import (
	"context"
	"errors"
	"github.com/tencent/codex-bridge/internal/bridge"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedUpdateAdmissionChecksWholeRun(t *testing.T) {
	s, st, uid, agent := newOrchestrationTestServer(t)
	c := testBridgeConn(agent, 8)
	s.pool.RegisterAgent(c)
	r := createOrchestrationRun(t, st, uid, agent)
	request := protocol.MustEnvelope(protocol.TypeReleaseHandoff, "", protocol.ReleaseHandoffPayload{Action: "request"})
	response := func() string {
		s.handleReleaseHandoff(context.Background(), c, request)
		h, _ := protocol.Decode[protocol.ReleaseHandoffPayload](<-c.send)
		return h.Action
	}
	for _, status := range []string{store.OrchestrationQueued, store.OrchestrationRunning, store.OrchestrationCanceling} {
		if err := st.UpdateOrchestrationRunStatus(context.Background(), r.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		if got := response(); got != "busy" || !s.pool.AgentOnline(agent) {
			t.Fatalf("admitted while %s: %s", status, got)
		}
	}
	if err := st.UpdateOrchestrationRunStatus(context.Background(), r.ID, store.OrchestrationCanceled, ""); err != nil {
		t.Fatal(err)
	}
	if got := response(); got != "granted" || s.pool.AgentOnline(agent) {
		t.Fatalf("idle handoff: %s", got)
	}
	if err := s.pool.SendToAgent(agent, protocol.MustEnvelope(protocol.TypeOrchestrationStart, "", nil)); err != ErrAgentOffline {
		t.Fatalf("new work during handoff: %v", err)
	}
	s.handleReleaseHandoff(context.Background(), c, protocol.MustEnvelope(protocol.TypeReleaseHandoff, "", protocol.ReleaseHandoffPayload{Action: "resume"}))
	if !s.pool.AgentOnline(agent) {
		t.Fatal("declined handoff did not restore admission")
	}
}

func TestManagedDownloadsRestrictFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "linux-amd64.json"), []byte("manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("1", 64) + ".bin"
	if err := os.Symlink(filepath.Join(dir, "linux-amd64.json"), filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		name   string
		status int
	}{{"linux-amd64.json", 200}, {"../key", 404}, {"key", 404}, {name, 404}} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		res := httptest.NewRecorder()
		serveManagedDownload(res, req, dir, check.name)
		if res.Code != check.status {
			t.Fatalf("%s: %d", check.name, res.Code)
		}
	}
}

// Exercise the registration fences with both ends of a real WebSocket. An
// idle gap between durable attempts still belongs to the active public run.
func TestManagedWorkerRegistrationAndIdleHandoff(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	s, st, uid, id := newOrchestrationTestServer(t)
	conn := httptest.NewServer(s)
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	token := store.NewToken("enr")
	if err := st.CreateEnrollTokenForUser(ctx, token, uid, "release-fixture", nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.Token = token
	cfg.Bridge.HubURL = conn.URL
	cfg.Bridge.CWD = root
	cfg.Bridge.MachineIDFile = filepath.Join(root, "machine")
	cfg.Bridge.CodexPath = filepath.Join(root, "no-codex")
	cfg.Bridge.ClaudePath = filepath.Join(root, "no-claude")
	if err := os.WriteFile(cfg.Bridge.MachineIDFile, []byte("machine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan struct{}, 1)
	registered := make(chan struct{})
	releaseStartup := make(chan struct{})
	c := bridge.NewClient(&cfg, "vManaged")
	c.ConfigureManagedRelease("instance", func() error {
		close(registered)
		select {
		case <-releaseStartup:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, func() error { return nil }, requests)
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-registered:
	case <-time.After(3 * time.Second):
		t.Fatal("registration not acknowledged")
	}
	if s.pool.AgentOnline(id) {
		t.Fatal("trial admitted work before its supervisor committed")
	}
	close(releaseStartup)
	waitUntil := func(predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if predicate() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("condition not reached")
	}
	waitUntil(func() bool { return s.pool.AgentOnline(id) })
	run := createOrchestrationRun(t, st, uid, id)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	requests <- struct{}{}
	select {
	case err := <-done:
		t.Fatalf("worker exited during durable run: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	if !s.pool.AgentOnline(id) {
		t.Fatal("busy endpoint was fenced")
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond * 15)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if !errors.Is(err, bridge.ErrReleaseDrained) {
				t.Fatal(err)
			}
			return
		case <-tick.C:
			select {
			case requests <- struct{}{}:
			default:
			}
		case <-deadline:
			t.Fatal("idle worker did not hand off")
		}
	}
}

func TestManagedStartupFencePreservesReconnectRecovery(t *testing.T) {
	s, st, uid, id := newOrchestrationTestServer(t)
	c := testBridgeConn(id, 8)
	c.releasePhase = "starting"
	s.pool.RegisterAgent(c)
	r := createOrchestrationRun(t, st, uid, id)
	if !s.pool.agentConnected(id) || s.pool.AgentOnline(id) {
		t.Fatal("connected startup state was not fenced")
	}
	s.recoverInterruptedRuns(context.Background())
	s.scheduleAgentRunFailure(id, "old-instance", time.Millisecond, "old transport")
	time.Sleep(30 * time.Millisecond)
	actual, err := st.OrchestrationRunByIDAnyUser(context.Background(), r.ID)
	if err != nil || actual.Status != store.OrchestrationQueued {
		t.Fatalf("active work touched by disconnect recovery during startup: %+v %v", actual, err)
	}
}
