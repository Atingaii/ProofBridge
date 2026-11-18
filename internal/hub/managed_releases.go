package hub

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"golang.org/x/sys/unix"
)

var releaseFilename = regexp.MustCompile(`^(?:(?:linux|darwin)-(?:amd64|arm64)\.json|[a-f0-9]{64}\.bin)$`)

func (s *Server) handleManagedDownload(w http.ResponseWriter, r *http.Request) {
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, "release directory unavailable", http.StatusInternalServerError)
		return
	}
	serveManagedDownload(w, r, filepath.Join(filepath.Dir(exe), "downloads", "releases"), r.PathValue("name"))
}
func serveManagedDownload(w http.ResponseWriter, r *http.Request, dir, name string) {
	if !releaseFilename.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".json") {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// Admission covers the same send queue for chats, task graphs and settings.
// A granted handoff follows everything already admitted to this connection.
func (c *BridgeConn) Send(env protocol.Envelope) error {
	c.admission.Lock()
	defer c.admission.Unlock()
	if c.releasePhase != "" {
		return ErrAgentOffline
	}
	return c.wsSender.Send(env)
}
func (c *BridgeConn) acceptingWork() bool {
	c.admission.Lock()
	defer c.admission.Unlock()
	return c.releasePhase == ""
}

func (s *Server) handleReleaseHandoff(ctx context.Context, c *BridgeConn, env protocol.Envelope) {
	h, err := protocol.Decode[protocol.ReleaseHandoffPayload](env)
	if err != nil {
		return
	}
	c.admission.Lock()
	switch h.Action {
	case "ready":
		if c.releasePhase == "starting" {
			if err := c.wsSender.Send(protocol.MustEnvelope(protocol.TypeReleaseHandoff, "", protocol.ReleaseHandoffPayload{Action: "ready"})); err == nil {
				c.releasePhase = "ready"
			}
		}
	case "serving":
		started := c.releasePhase == "ready"
		if started {
			c.releasePhase = ""
		}
		c.admission.Unlock()
		if started {
			s.dispatchReadyTaskGraphsForAgent(ctx, c.agentID)
			s.scheduleHistoricalUsageBackfill(c.agentID)
		}
		return
	case "resume":
		resumed := c.releasePhase == "draining"
		if resumed {
			c.releasePhase = ""
		}
		c.admission.Unlock()
		if resumed {
			s.dispatchReadyTaskGraphsForAgent(ctx, c.agentID)
		}
		return
	case "request":
		phase := c.releasePhase
		c.admission.Unlock()
		if phase == "draining" {
			c.releaseReply("granted")
			return
		}
		if phase == "" {
			s.grantIdleHandoff(ctx, c)
		}
		return
	}
	c.admission.Unlock()
}

func (c *BridgeConn) releaseReply(action string) error {
	return c.wsSender.Send(protocol.MustEnvelope(protocol.TypeReleaseHandoff, "", protocol.ReleaseHandoffPayload{Action: action}))
}

func (s *Server) grantIdleHandoff(ctx context.Context, c *BridgeConn) {
	// The normal busy case keeps accepting work. Recheck after fencing to
	// catch work admitted between the query and the admission lock.
	busy, err := s.store.AgentHasActiveWork(ctx, c.agentID)
	if busy || err != nil {
		_ = c.releaseReply("busy")
		return
	}
	c.admission.Lock()
	if c.releasePhase != "" {
		c.admission.Unlock()
		return
	}
	c.releasePhase = "checking"
	c.admission.Unlock()
	busy, err = s.store.AgentHasActiveWork(ctx, c.agentID)
	c.admission.Lock()
	defer c.admission.Unlock()
	if busy || err != nil {
		c.releasePhase = ""
		_ = c.releaseReply("busy")
		return
	}
	c.releasePhase = "draining"
	if err = c.releaseReply("granted"); err != nil {
		c.releasePhase = ""
	}
}
