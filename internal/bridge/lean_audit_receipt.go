package bridge

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

var errLeanAuditAccepted = errors.New("current independent Lean submission audit accepted")

type leanInputs struct {
	Digest string
	Frozen string
	Proofs map[string]string
}

type leanReceipt struct {
	Digest   string
	Command  string
	Output   string
	Reviewer string
}

type leanAuditScope struct {
	root     string
	path     string
	original leanInputs
}

func isLakeProject(root string) bool {
	for _, name := range []string{"lakefile.toml", "lakefile.lean", "lean-toolchain"} {
		if info, err := os.Stat(filepath.Join(root, name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func leanSubmissionTask(p protocol.OrchestrationStartPayload, root string) bool {
	return p.TaskGraph != nil && normalizeOrchestrationProfile(p.Profile) == "formal-proof" && strings.Contains(exactOrchestrationTaskPrompt(p), "final-audit") && isLakeProject(root)
}

// Keep every explicitly frozen manifest entry in the proof identity, including
// log-shaped inputs. The evaluator checks their checksums during submission.
func leanFrozenFiles(root string) (map[string]bool, error) {
	path := filepath.Join(root, "spec.sha256")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("invalid frozen manifest")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names := make(map[string]bool)
	scan := bufio.NewScanner(io.LimitReader(f, (1<<20)+1))
	for scan.Scan() {
		line := scan.Text()
		if len(line) > 66 && line[64] == ' ' && (line[65] == ' ' || line[65] == '*') {
			name := strings.TrimPrefix(line[66:], "./")
			names[name] = true
		}
	}
	return names, scan.Err()
}

// The archive evaluator scans every local Lean source (including helpers in
// validation/). Only generated report files there and compiler caches are
// excluded. Build files, evaluator programs, manifests and frozen inputs count.
func fingerprintLeanInputs(ctx context.Context, root string, notAfter time.Time) (leanInputs, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	all, frozen := sha256.New(), sha256.New()
	inputs := leanInputs{Proofs: make(map[string]string)}
	files, remaining := 0, int64(32<<20)
	manifest, err := leanFrozenFiles(root)
	if err != nil {
		return leanInputs{}, err
	}
	err = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if e.IsDir() {
			switch e.Name() {
			case ".git", ".codex-bridge", ".codex", ".claude", ".lake", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(e.Name())
		name := strings.ToLower(rel)
		rootAuditLog := manifest != nil && !strings.Contains(rel, "/") && ext == ".log" && strings.Contains(name, "final") && strings.Contains(name, "audit")
		report := strings.HasPrefix(rel, "validation/") && (ext == ".log" || ext == ".txt")
		if !manifest[rel] && (rootAuditLog || report) {
			return nil
		}
		switch ext {
		case ".olean", ".ilean", ".o", ".pyc":
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || files >= 4096 || info.Size() > remaining {
			return errors.New("cannot establish complete Lean input digest")
		}
		if !notAfter.IsZero() && info.ModTime().After(notAfter) {
			return errors.New("input was written during the audit")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, remaining+1))
		f.Close()
		if err != nil {
			return err
		}
		if n != info.Size() || n > remaining {
			return errors.New("Lean input changed while being read")
		}
		files++
		remaining -= n
		sum := hex.EncodeToString(h.Sum(nil))
		fmt.Fprintf(all, "%s\x00%s\n", rel, sum)
		if ext == ".lean" {
			inputs.Proofs[rel] = sum
		}
		if manifest[rel] || rel == "GNUmakefile" || rel == "makefile" || rel == "Makefile" || ext == ".mk" || rel == "README.md" || rel == "spec.sha256" || rel == "audit-config.json" || rel == "lean-toolchain" || strings.HasPrefix(rel, "tools/") {
			fmt.Fprintf(frozen, "%s\x00%s\n", rel, sum)
		}
		return nil
	})
	if err != nil {
		return leanInputs{}, err
	}
	if len(inputs.Proofs) == 0 {
		return leanInputs{}, errors.New("no Lean source files")
	}
	inputs.Digest = hex.EncodeToString(all.Sum(nil))
	inputs.Frozen = hex.EncodeToString(frozen.Sum(nil))
	return inputs, nil
}

// Anchor the original evaluator before any task node can change it. The state
// directory is outside the submitted project; uploading/copying an audit log
// cannot manufacture a Bridge receipt. Hard linking the initial snapshot uses
// first-writer-wins across concurrent graph nodes and process restarts.
func (m *OrchestrationManager) leanSubmissionScope(ctx context.Context, p protocol.OrchestrationStartPayload, root string) *leanAuditScope {
	if !leanSubmissionTask(p, root) {
		return nil
	}
	s := &leanAuditScope{root: root}
	resolved, err := canonicalWorkspace(root)
	if err != nil {
		return s
	}
	root, s.root = resolved, resolved
	public := m.executionFor(p.RunID).runID
	if public == "" {
		public = p.RunID
	}
	identity := fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s", public, p.PromptSeq, p.InputVersion, root, exactOrchestrationTaskPrompt(p))
	id := sha256.Sum256([]byte(identity))
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return s
		}
		state = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(state, "proofbridge", "lean-audits", hex.EncodeToString(id[:]))
	if !filepath.IsAbs(dir) || pathWithin(root, dir) || os.MkdirAll(dir, 0700) != nil {
		return s
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil || pathWithin(root, realDir) {
		return s
	}
	s.path = realDir
	frozenFile := filepath.Join(realDir, "inputs.json")
	if _, err := os.Stat(frozenFile); os.IsNotExist(err) {
		inputs, err := fingerprintLeanInputs(ctx, root, time.Time{})
		if err != nil {
			return s
		}
		if err = writeLeanAuditState(frozenFile, inputs, true); err != nil && !os.IsExist(err) {
			return s
		}
	}
	readLeanAuditState(frozenFile, &s.original)
	return s
}

func writeLeanAuditState(path string, value any, first bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".audit-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(value)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if first {
		return os.Link(f.Name(), path)
	}
	return os.Rename(f.Name(), path)
}

func readLeanAuditState(path string, dest any) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(dest) == nil
}

func (s *leanAuditScope) current(ctx context.Context, cutoff time.Time) (leanInputs, bool) {
	if s == nil || s.original.Frozen == "" {
		return leanInputs{}, false
	}
	in, err := fingerprintLeanInputs(ctx, s.root, cutoff)
	return in, err == nil && in.Frozen == s.original.Frozen
}

type leanAuditStart struct {
	at     time.Time
	digest string
}

type leanReviewTurn struct {
	mu            sync.Mutex
	scope         *leanAuditScope
	ctx           context.Context
	cancel        context.CancelCauseFunc
	turnStart     time.Time
	initial       string
	started       map[string]leanAuditStart
	active        map[string]bool
	receipt       leanReceipt
	accepted      bool
	pending       bool
	failure       *protocol.VerifierCheck
	failureDigest string
}

func (r *leanReviewTurn) observe(t RunnerToolEvent, reviewer string) {
	if t.Progress || t.ID == "" || r.ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.accepted || r.ctx.Err() != nil {
		return
	}
	c, recognized := parseLeanAuditCommand(t.Command)
	if isRunningToolStatus(t.Status) {
		r.active[t.ID] = true
		if recognized && !c.adopt {
			at := time.Now()
			input, ok := r.scope.current(r.ctx, at)
			if ok {
				r.started[t.ID] = leanAuditStart{at: at, digest: input.Digest}
			}
		}
		return
	}
	delete(r.active, t.ID)
	start := r.started[t.ID]
	delete(r.started, t.ID)
	if !recognized {
		if !auditToolSucceeded(t) {
			r.pending = false
		}
		r.settle()
		return
	}
	r.pending = false
	directory := firstNonEmpty(c.directory, t.CWD)
	if !c.adopt || directory != "" {
		actual, err := canonicalWorkspace(directory)
		if err != nil || actual != r.scope.root {
			return
		}
	}
	if c.adopt {
		if !c.passed(t) {
			return
		}
		in, ok := r.scope.current(r.ctx, time.Time{})
		if !ok || !r.loadReceipt(in.Digest) {
			return
		}
	} else {
		if start.at.IsZero() {
			start = leanAuditStart{at: r.turnStart, digest: r.initial}
		}
		in, ok := r.scope.current(r.ctx, start.at)
		if !ok || in.Digest != start.digest {
			return
		}
		if !c.passed(t) {
			// Clear any previous result when the approved evaluator is observed
			// failing again on this checkout. Missing exit/progress cannot pass.
			r.receipt = leanReceipt{}
			if r.scope.path != "" {
				_ = os.Remove(filepath.Join(r.scope.path, "receipt.json"))
			}
			if check := leanProofHole(t.Output); check != nil {
				r.failure = check
				r.failureDigest = in.Digest
			}
			return
		}
		r.failure = nil
		output := strings.TrimSpace(t.Output)
		if len(output) > 2300 {
			output = output[len(output)-2300:]
		}
		r.receipt = leanReceipt{Digest: in.Digest, Command: t.Command, Output: output, Reviewer: reviewer}
		_ = writeLeanAuditState(filepath.Join(r.scope.path, "receipt.json"), r.receipt, false)
	}
	if c.final {
		r.pending = true
		r.settle()
	}

}

// Other native commands can be in flight when the final evaluator exits.
// Join their completed observations, then revalidate the same recorded input.
func (r *leanReviewTurn) settle() {
	if !r.pending || len(r.active) > 0 {
		return
	}
	r.pending = false
	in, ok := r.scope.current(r.ctx, time.Time{})
	if !ok || in.Digest != r.receipt.Digest {
		return
	}
	r.accepted = true
	r.cancel(errLeanAuditAccepted)
}

func leanProofHole(output string) *protocol.VerifierCheck {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ERROR: ") && strings.Contains(line, ".lean:") && strings.Contains(line, "proof hole is not allowed") {
			target, _, _ := strings.Cut(strings.TrimPrefix(line, "ERROR: "), ": ")
			return &protocol.VerifierCheck{Name: "verification", Code: "proof-incomplete", Target: target, Reason: "independent submission evaluator found a proof hole", Evidence: line, Status: verifierVerdictContinue}
		}
	}
	return nil
}

func (r *leanReviewTurn) loadReceipt(digest string) bool {
	if digest == "" || r.scope.path == "" {
		return false
	}
	var receipt leanReceipt
	if !readLeanAuditState(filepath.Join(r.scope.path, "receipt.json"), &receipt) || receipt.Digest != digest || receipt.Reviewer == "" {
		return false
	}
	r.receipt = receipt
	return true
}

func (s *leanAuditScope) verdict(ctx context.Context, r leanReceipt) (protocol.VerifierVerdict, bool) {
	in, ok := s.current(ctx, time.Time{})
	if !ok || in.Digest != r.Digest {
		return protocol.VerifierVerdict{}, false
	}
	checks := make([]protocol.VerifierCheck, 0, len(verifierCheckNames))
	for _, name := range verifierCheckNames {
		checks = append(checks, protocol.VerifierCheck{Name: name, Status: verifierVerdictPass, Reason: "independent reviewer confirmed the current submission", Evidence: r.Command + "\n" + leanSubmissionMarker})
	}
	return protocol.VerifierVerdict{Status: verifierVerdictPass, Reason: "independent Lean submission audit passed for the current sources", Evidence: []string{r.Command, "make final-audit exit 0", leanSubmissionMarker, "source sha256 " + in.Digest, "reviewer " + r.Reviewer}, Checkers: checks}, true
}

func (s *leanAuditScope) summary(ctx context.Context, r leanReceipt) string {
	in, ok := s.current(ctx, time.Time{})
	var changed []string
	if ok {
		for file, hash := range in.Proofs {
			if s.original.Proofs[file] != hash {
				changed = append(changed, file)
			}
		}
		for file := range s.original.Proofs {
			if _, ok := in.Proofs[file]; !ok {
				changed = append(changed, file)
			}
		}
		sort.Strings(changed)
	}
	files := "无"
	if len(changed) > 0 {
		files = strings.Join(changed, ", ")
	}
	return fmt.Sprintf("独立审核员已确认其余任务要求，提交终审通过。\n命令：%s\n评测器退出码：0\n变更的 Lean 源文件：%s\n审核输出：\n%s\n剩余证明义务：无。", r.Command, files, r.Output)
}

const leanReviewerGuidance = "\nIndependent Lean reviewer: finish the original task's semantic, frozen-source, architecture and trust checks first. When only the final evaluator remains and its success settles the whole task, run exactly `cd %s && env PROOFBRIDGE_FINAL_REVIEW=1 timeout --kill-after=10s 24h make -j1 final-audit` from the current project as a single foreground command. Bridge captures its output, so no tee, redirect or repeated build is needed. If you already ran an authoritative current submission audit and have now confirmed the other requirements, run `: PROOFBRIDGE_FINAL_REVIEW` to adopt the recorded evidence. Use a normal final handoff if no receipt is available. A failed command never completes the task.\n"

func (m *OrchestrationManager) reviewedRelayTurn(ctx context.Context, s *leanAuditScope, p protocol.OrchestrationStartPayload, turnID, role, cli, slot, prompt string, state *orchestrationSessionState, root string) (orchestrationTurn, string, error) {
	if s == nil || role != store.TaskRoleReviewer || !shouldRunAgentVerifier(p) {
		return m.runRelayTurnWithContinuations(ctx, p, turnID, role, cli, slot, prompt, state, root)
	}
	turnCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	r := &leanReviewTurn{scope: s, ctx: turnCtx, cancel: cancel, turnStart: time.Now(), started: map[string]leanAuditStart{}, active: map[string]bool{}}
	if inputs, ok := s.current(ctx, time.Time{}); ok {
		r.initial = inputs.Digest
	}
	m.mu.Lock()
	if m.leanReviewers == nil {
		m.leanReviewers = make(map[string]*leanReviewTurn)
	}
	m.leanReviewers[turnID] = r
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.leanReviewers, turnID); m.mu.Unlock() }()
	record, status, err := m.runRelayTurnWithContinuations(turnCtx, p, turnID, role, cli, slot, prompt, state, root)
	r.mu.Lock()
	accepted, receipt, failed, failedDigest, active := r.accepted, r.receipt, r.failure, r.failureDigest, len(r.active)
	r.mu.Unlock()
	if errors.Is(context.Cause(turnCtx), errLeanAuditAccepted) {
		// Closing the single reviewer task session also stops an app-server
		// turn that outlives its local event subscription.
		m.closeNativeSession(p.RunID)
		if ctx.Err() != nil {
			return record, status, ctx.Err()
		}
		if _, ok := s.verdict(ctx, receipt); !ok {
			return record, status, errors.New("Lean inputs changed while committing the reviewer decision")
		}
		record.Err = ""
		record.Content = s.summary(ctx, receipt)
		status, err = "success", nil
	}
	if err == nil && ctx.Err() == nil && active == 0 {
		confirm := !reviewerHasOutstandingWork(record.Relay) && strings.TrimSpace(record.Content) != ""
		// A resolved independent handoff may also adopt an earlier native
		// receipt across nodes or a Bridge restart. Log names are irrelevant.
		if confirm && receipt.Digest == "" && record.Relay.Status == "resolved" {
			in, ok := s.current(ctx, time.Time{})
			if ok && s.path != "" {
				var saved leanReceipt
				if readLeanAuditState(filepath.Join(s.path, "receipt.json"), &saved) && saved.Reviewer != "" && saved.Digest == in.Digest {
					receipt = saved
				}
			}
		}
		if receipt.Digest != "" && (accepted || confirm) {
			record.LeanReceipt = &receipt
		}
	}
	if err == nil && failed != nil {
		if in, ok := s.current(ctx, time.Time{}); ok && in.Digest == failedDigest {
			record.LeanFailure = failed
		}
	}
	return record, status, err
}

func reviewerHasOutstandingWork(p orchestrationRelayPacket) bool {
	return p.Structured && (p.Status == "blocked" || p.Status == "needs_next" || (p.Next != "" && !machineExplicitNone(p.Next)) || (p.Risks != "" && !machineExplicitNone(p.Risks)))
}

func verificationSourceDigest(ctx context.Context, root string, scope *leanAuditScope) string {
	if scope == nil {
		return completionSourceDigest(ctx, root)
	}
	in, ok := scope.current(ctx, time.Time{})
	if !ok {
		return ""
	}
	return in.Digest
}

func reviewerCompletionAudit(ctx context.Context, p protocol.OrchestrationStartPayload, record orchestrationTurn, scope *leanAuditScope) (protocol.VerifierVerdict, bool) {
	if scope == nil {
		return reviewerFinalAuditVerdict(ctx, p, record)
	}
	if !shouldRunAgentVerifier(p) || record.Role != store.TaskRoleReviewer || record.Err != "" {
		return protocol.VerifierVerdict{}, false
	}
	if record.LeanReceipt != nil {
		return scope.verdict(ctx, *record.LeanReceipt)
	}
	if record.LeanFailure != nil {
		return protocol.VerifierVerdict{Status: verifierVerdictContinue, Reason: record.LeanFailure.Reason, Checkers: []protocol.VerifierCheck{*record.LeanFailure}}, true
	}
	return protocol.VerifierVerdict{}, false
}
