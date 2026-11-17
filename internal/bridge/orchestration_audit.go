package bridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func defaultVerifierTimeouts() []time.Duration {
	var limits []time.Duration
	for limit := verifierAgentTimeout; ; limit *= 2 {
		if limit >= verifierMaxTimeout {
			return append(limits, verifierMaxTimeout)
		}
		limits = append(limits, limit)
	}
}

func (m *OrchestrationManager) runAgentVerifier(ctx context.Context, payload protocol.OrchestrationStartPayload, assignment orchestrationTurnAssignment, index int, prompt string) agentVerifierResult {
	select {
	case agentVerificationGate <- struct{}{}:
		defer func() { <-agentVerificationGate }()
	case <-ctx.Done():
		return agentVerifierResult{Agent: fmt.Sprintf("agent-%d", index), Slot: assignment.WorkerSlot, CLI: assignment.CLI, Status: verifierVerdictContinue, Reason: "verification canceled while waiting for host capacity", Error: ctx.Err().Error()}
	}
	limits := m.verifierTimeouts
	if len(limits) == 0 {
		limits = defaultVerifierTimeouts()
	}
	var usage []orchestrationUsage
	var result agentVerifierResult
	for attempt, limit := range limits {
		if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
			return result
		}
		attemptPrompt := prompt + fmt.Sprintf("\nBridge audit attempt %d: execution allowance %s. Retry only the audit, without source changes or a new worker round. Use this increased allowance for any command that previously timed out.\n", attempt+1, limit)
		result = m.runAgentVerifierAttempt(ctx, payload, assignment, index, attemptPrompt, limit)
		usage = append(usage, result.Usage)
		result.Usage = combineModelUsage(usage)
		result.Attempts = attempt + 1
		result.TimeoutSeconds = int(limit.Seconds())
		if !result.TimedOut || ctx.Err() != nil {
			return result
		}
		if attempt+1 < len(limits) {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "bridge.note", Source: "bridge", Role: "verifier", Content: fmt.Sprintf("独立审核超时（%s），仅重试同一审核，时限增加至 %s；不进入代码修改轮。", limit, limits[attempt+1]), BridgeNoteData: &protocol.BridgeNoteData{Category: "verification-timeout-retry"}})
		}
	}
	result.Reason = "independent audit exhausted its maximum timeout; verification is incomplete"
	result.Error = result.Reason
	return result
}

func auditTimeoutText(s string) bool {
	lower := strings.ToLower(s)
	for _, token := range []string{"timeout", "timed out", "deadline exceeded", "超时", "exit status 124", "exit code 124", "exit_code=124"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func reportedAuditTimeout(result agentVerifierResult) bool {
	if result.Status == verifierVerdictPass {
		return false
	}
	timeout := false
	for _, check := range result.Checks {
		if check.Status == verifierVerdictPass {
			continue
		}
		isTimeout := auditTimeoutText(check.Reason + " " + check.Evidence)
		// Actual missing targets, specification conflicts and trust violations must
		// not be hidden by an unrelated timeout in the same response.
		if concreteVerifierBlocker(check) && !((check.Code == "build-failed" || check.Code == "proof-incomplete") && isTimeout) {
			return false
		}
		timeout = timeout || isTimeout
	}
	return timeout || auditTimeoutText(result.Reason)
}

func verifierTimeoutExhausted(results []agentVerifierResult) bool {
	for _, result := range results {
		if result.TimedOut {
			return true
		}
	}
	return false
}

var finalAuditMarker = regexp.MustCompile(`(?m)^FINAL AUDIT PASSED(?:[ (:].*)?\.?\r?$`)

// Deliberately accept only a direct final-audit execution. Shell fragments,
// echoes, grep/cat of old logs and pipelines cannot manufacture this receipt.
var finalAuditCommand = regexp.MustCompile(`^(?:timeout (?:--kill-after=[0-9]+[smhd]? )?[0-9]+[smhd]? )?(?:make (?:-j ?1 )?final-audit|(?:bash |sh )?(?:\./)?tools/final_audit\.sh(?: --(?:submission|final))?)(?:\s+(?:1?>|2>|2>&1)\s*[^;&|<>]*)*$`)

func reviewerFinalAuditVerdict(ctx context.Context, payload protocol.OrchestrationStartPayload, record orchestrationTurn) (protocol.VerifierVerdict, bool) {
	p := record.Relay
	if !shouldRunAgentVerifier(payload) || record.Role != store.TaskRoleReviewer || record.Err != "" || !p.Structured || p.Status != "resolved" || p.To != "user" || p.Intent != "final" || !machineExplicitNone(p.Next) || !machineExplicitNone(p.Risks) {
		return protocol.VerifierVerdict{}, false
	}
	// The audit must be the last tool execution, not an earlier successful probe
	// followed by source edits or a failed check. Ignore deltas for that same id.
	var last *RunnerToolEvent
	for i := range record.Tools {
		tool := &record.Tools[i]
		if tool.Progress {
			continue
		}
		last = tool
	}
	if last == nil || last.ExitCode == nil || *last.ExitCode != 0 || (last.Status != "completed" && last.Status != "success" && last.Status != "succeeded") || !finalAuditCommand.MatchString(strings.TrimSpace(last.Command)) || !finalAuditMarker.MatchString(finalAuditOutput(payload, *last)) {
		return protocol.VerifierVerdict{}, false
	}
	if last.CompletedAt.IsZero() || completionSourceDigestBefore(ctx, firstNonEmpty(payload.RunCWD, payload.CWD), last.CompletedAt) == "" {
		return protocol.VerifierVerdict{}, false
	}
	checks := make([]protocol.VerifierCheck, 0, 3)
	for _, name := range verifierCheckNames {
		checks = append(checks, protocol.VerifierCheck{Name: name, Status: verifierVerdictPass, Reason: "independent reviewer confirmed the original task with a successful final audit", Evidence: last.Command + "\nFINAL AUDIT PASSED"})
	}
	return protocol.VerifierVerdict{Status: verifierVerdictPass, Reason: "independent reviewer final audit passed; no duplicate workspace audit is required", Evidence: []string{last.Command, "FINAL AUDIT PASSED", "resolved independent reviewer final handoff"}, Checkers: checks}, true
}

var finalAuditRedirect = regexp.MustCompile(`(?:^|\s)1?>\s*([a-zA-Z0-9_./-]+)(?:\s|$)`)

// Native exit zero certifies the audit command, not a later cat/grep command.
// Read only a fresh, regular log inside the same workspace, never a symlink.
func finalAuditOutput(payload protocol.OrchestrationStartPayload, tool RunnerToolEvent) string {
	if finalAuditMarker.MatchString(tool.Output) {
		return tool.Output
	}
	match := finalAuditRedirect.FindStringSubmatch(tool.Command)
	if len(match) != 2 || tool.StartedAt.IsZero() || tool.CompletedAt.IsZero() {
		return ""
	}
	cwd, err := filepath.Abs(firstNonEmpty(payload.RunCWD, payload.CWD))
	if err != nil {
		return ""
	}
	path := match[1]
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	originalInfo, err := os.Lstat(path)
	if err != nil || originalInfo.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil || !strictPathWithin(cwd, path) {
		return ""
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 || info.ModTime().Before(tool.StartedAt) || info.ModTime().After(tool.CompletedAt) {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
