package bridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func defaultVerifierTimeouts() []time.Duration {
	// Give slow final audits their full allowance immediately. A single retry
	// recovers a model/tool timeout without restarting the source-editing round.
	return []time.Duration{verifierMaxTimeout, verifierMaxTimeout}
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
		attemptPrompt := prompt + fmt.Sprintf("\nBridge audit attempt %d: execution allowance %s. Retry only the audit, without source changes or a new worker round. Use the current allowance for any command that previously timed out.\n", attempt+1, limit)
		result = m.runAgentVerifierAttempt(ctx, payload, assignment, index, attemptPrompt, limit)
		usage = append(usage, result.Usage)
		result.Usage = combineModelUsage(usage)
		result.Attempts = attempt + 1
		result.TimeoutSeconds = int(limit.Seconds())
		if !result.TimedOut || ctx.Err() != nil {
			return result
		}
		if attempt+1 < len(limits) {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "bridge.note", Source: "bridge", Role: "verifier", Content: fmt.Sprintf("独立审核超时（%s），仅重试同一审核，下一次时限为 %s；不进入代码修改轮。", limit, limits[attempt+1]), BridgeNoteData: &protocol.BridgeNoteData{Category: "verification-timeout-retry"}})
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

// Direct final-audit executions use this shape. Independent log reads have
// their own source-freshness path; echoes and pipelines cannot create a receipt.
var finalAuditCommand = regexp.MustCompile(`^(?:timeout (?:--kill-after=[0-9]+[smhd]? )?[0-9]+[smhd]? )?(?:make (?:-j ?1 )?final-audit|(?:bash |sh )?(?:\./)?tools/final_audit\.sh(?: --(?:submission|final))?)(?:\s+(?:1?>|2>|2>&1)\s*[^;&|<>]*)*$`)

func reviewerFinalAuditVerdict(ctx context.Context, payload protocol.OrchestrationStartPayload, record orchestrationTurn) (protocol.VerifierVerdict, bool) {
	p := record.Relay
	if !shouldRunAgentVerifier(payload) || record.Role != store.TaskRoleReviewer || record.Err != "" {
		return protocol.VerifierVerdict{}, false
	}
	// Formatting is not evidence. Preserve explicit contradictory obligations,
	// while accepting plain-language summaries and peer-directed handoffs.
	if p.Structured && (p.Status == "blocked" || (p.Next != "" && !machineExplicitNone(p.Next)) || (p.Risks != "" && !machineExplicitNone(p.Risks))) {
		return protocol.VerifierVerdict{}, false
	}
	cwd := firstNonEmpty(payload.RunCWD, payload.CWD)
	for i := len(record.Tools) - 1; i >= 0; i-- {
		tool := record.Tools[i]
		tool.Command = auditCommandText(tool.Command)
		if tool.Progress {
			continue
		}
		if !auditToolSucceeded(tool) {
			return protocol.VerifierVerdict{}, false
		}
		auditTime := time.Time{}
		if tool.ExitCode != nil && *tool.ExitCode == 0 && finalAuditCommand.MatchString(strings.TrimSpace(tool.Command)) && finalAuditMarker.MatchString(finalAuditOutput(payload, tool)) {
			auditTime = tool.CompletedAt
		} else if logTime, ok := independentFinalAuditLog(cwd, tool); ok {
			auditTime = logTime
		}
		if !auditTime.IsZero() && completionSourceDigestBefore(ctx, cwd, auditTime) != "" {
			checks := make([]protocol.VerifierCheck, 0, len(verifierCheckNames))
			for _, name := range verifierCheckNames {
				checks = append(checks, protocol.VerifierCheck{Name: name, Status: verifierVerdictPass, Reason: "independent reviewer final-audit evidence covers the current sources", Evidence: tool.Command + "\nFINAL AUDIT PASSED"})
			}
			return protocol.VerifierVerdict{Status: verifierVerdictPass, Reason: "independent reviewer final audit passed; no duplicate workspace audit is required", Evidence: []string{tool.Command, "FINAL AUDIT PASSED", "current source freshness confirmed"}, Checkers: checks}, true
		}
		if !auditReadOnlyFollowup(cwd, tool.Command) {
			return protocol.VerifierVerdict{}, false
		}
	}
	return protocol.VerifierVerdict{}, false
}

// Native CLIs may render a shell argv as a single command string. Remove one
// conventional shell wrapper, then apply the same command/log checks as usual.
func auditCommandText(command string) string {
	command = strings.TrimSpace(command)
	for _, prefix := range []string{"/bin/bash -lc ", "/bin/bash -c ", "bash -lc ", "bash -c ", "/bin/sh -lc ", "/bin/sh -c ", "sh -lc ", "sh -c "} {
		if !strings.HasPrefix(command, prefix) {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(command, prefix))
		if len(body) >= 2 && ((body[0] == '\'' && body[len(body)-1] == '\'') || (body[0] == '"' && body[len(body)-1] == '"')) {
			body = body[1 : len(body)-1]
		}
		return strings.TrimSpace(body)
	}
	return command
}

func auditToolSucceeded(tool RunnerToolEvent) bool {
	if tool.ExitCode != nil && *tool.ExitCode != 0 {
		return false
	}
	return tool.Status == "completed" || tool.Status == "success" || tool.Status == "succeeded"
}

func auditReadOnlyFollowup(cwd, command string) bool {
	if isClaudeReadCommand(command) {
		return true
	}
	args, err := splitSimpleCommand(command)
	if err != nil || len(args) == 0 {
		return false
	}
	switch args[0] {
	case "cat":
		return validateCatArgs(cwd, args[1:])
	case "pwd":
		return len(args) == 1
	case "git":
		return len(args) >= 2 && args[1] == "status"
	case "head", "tail":
		files := 0
		for i := 1; i < len(args); i++ {
			if args[i] == "-n" || args[i] == "-c" {
				i++
				if i >= len(args) {
					return false
				}
				if _, err := strconv.ParseUint(args[i], 10, 32); err != nil {
					return false
				}
			} else if strings.HasPrefix(args[i], "-") || !workspacePath(cwd, args[i], true, true) {
				return false
			} else {
				files++
			}
		}
		return files > 0
	}
	return false
}

// A reviewer can reuse an existing independent log without rerunning its audit.
// The log must be inside this workspace, contain the final pass marker, predate
// the successful read, and be at least as new as every source file.
func independentFinalAuditLog(cwd string, tool RunnerToolEvent) (time.Time, bool) {
	args, err := splitSimpleCommand(tool.Command)
	if err != nil || len(args) != 2 || (args[0] != "cat" && args[0] != "Read") || tool.CompletedAt.IsZero() {
		return time.Time{}, false
	}
	if args[0] == "cat" && (tool.ExitCode == nil || *tool.ExitCode != 0) {
		return time.Time{}, false
	}
	name := strings.ToLower(filepath.Base(args[1]))
	independent := strings.Contains(name, "review") || strings.Contains(name, "independent") || strings.Contains(name, "verifier")
	if !independent || !strings.Contains(name, "final") || !strings.Contains(name, "audit") || !strings.HasSuffix(name, ".log") {
		return time.Time{}, false
	}
	root, err := canonicalWorkspace(cwd)
	if err != nil {
		return time.Time{}, false
	}
	path := args[1]
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 || info.ModTime().After(tool.CompletedAt) {
		return time.Time{}, false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !strictPathWithin(root, resolved) {
		return time.Time{}, false
	}
	raw, err := os.ReadFile(resolved)
	if err != nil || !finalAuditMarker.Match(raw) {
		return time.Time{}, false
	}
	return info.ModTime(), true
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
