package hub

import (
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

const modelRecoveryRetrySeconds int64 = 6 * 60 * 60

func modelRecoveryDue(hints []protocol.OrchestrationUsageRecovery, syncs []store.OrchestrationUsageSync, now int64) bool {
	for _, hint := range hints {
		latest := int64(0)
		for _, sync := range syncs {
			if sync.SessionID == hint.RecoveryMarkerID() && sync.ScannedAt > latest {
				latest = sync.ScannedAt
			}
		}
		if latest == 0 || now-latest >= modelRecoveryRetrySeconds {
			return true
		}
	}
	return false
}

func (s *Server) supportsModelRecovery(agentID string) bool {
	caps, ok := s.pool.AgentCapabilities(agentID)
	return ok && caps.UsageModelRecovery >= 2
}

// Reserve before dispatch so concurrent reads/reconnects cannot fan out scans.
func (s *Server) reserveUsageSync(runID string, now time.Time) bool {
	s.usageSyncMu.Lock()
	defer s.usageSyncMu.Unlock()
	if s.usageSyncRequested == nil {
		s.usageSyncRequested = map[string]time.Time{}
	}
	if prior, ok := s.usageSyncRequested[runID]; ok && now.Sub(prior) < 2*time.Minute {
		return false
	}
	for id, at := range s.usageSyncRequested {
		if now.Sub(at) >= 2*time.Minute {
			delete(s.usageSyncRequested, id)
		}
	}
	s.usageSyncRequested[runID] = now
	return true
}

func (s *Server) releaseUsageSync(runID string) {
	s.usageSyncMu.Lock()
	defer s.usageSyncMu.Unlock()
	delete(s.usageSyncRequested, runID)
}

func (s *Server) modelRecoveryStatus(agentID string, hints []protocol.OrchestrationUsageRecovery, syncs []store.OrchestrationUsageSync, now int64) string {
	if len(hints) == 0 {
		return ""
	}
	if _, ok := s.pool.AgentConnectionInfo(agentID); !ok {
		return "offline"
	}
	if !s.supportsModelRecovery(agentID) {
		return "upgrade-required"
	}
	if modelRecoveryDue(hints, syncs, now) {
		return "pending"
	}
	return "unresolved"
}

// Recovery bookkeeping must never change the choice of accounting source.
func accountingUsageSyncs(syncs []store.OrchestrationUsageSync) []store.OrchestrationUsageSync {
	result := make([]store.OrchestrationUsageSync, 0, len(syncs))
	for _, item := range syncs {
		if !strings.HasPrefix(item.SessionID, "model-recovery-v2-") {
			result = append(result, item)
		}
	}
	return result
}
