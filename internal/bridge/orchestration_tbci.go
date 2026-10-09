package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

const (
	tbciMaxSnapshotBytes = 512 << 20
	tbciMaxSnapshotFiles = 100000
)

var tbciVerificationGate = make(chan struct{}, 1)

type tbciVerificationResult struct {
	Attempted bool
	Trusted   bool
	Verdict   string
	Reason    string
	Error     error
}

type tbciVerificationSession struct {
	runtime        string
	initial        string
	contract       string
	verifierPriv   string
	adapter        string
	available      bool
	unavailableWhy string
	workspace      string
	taskDigest     string
	templateSource string
}

type tbciSessionMetadata struct {
	SchemaVersion  string `json:"schemaVersion"`
	Workspace      string `json:"workspace"`
	TaskDigest     string `json:"taskDigest"`
	Adapter        string `json:"adapter"`
	TemplateSource string `json:"templateSource"`
}

type tbciTargetCandidate struct {
	Prover string
	Module string
	Name   string
	Path   string
}

type tbciTemplate struct {
	SchemaVersion string `json:"schema_version"`
	Goal          struct {
		Prover string `json:"prover"`
		Module string `json:"module"`
		Name   string `json:"name"`
	} `json:"goal"`
	AuthorizedPaths []string       `json:"authorized_paths"`
	Trust           map[string]any `json:"trust"`
	Environment     map[string]any `json:"environment"`
	Policy          struct {
		RequiredEvidence           []string `json:"required_evidence"`
		RequireVerifierObservation bool     `json:"require_verifier_observation"`
	} `json:"policy"`
	Verification struct {
		Predicate string `json:"predicate"`
		Checker   string `json:"checker"`
	} `json:"verification"`
}

func shouldRunTBCIVerifier(profile, verificationMode string, payload protocol.OrchestrationStartPayload) bool {
	return normalizeOrchestrationProfile(profile) == "formal-proof" &&
		protocol.NormalizeVerificationMode(verificationMode) == protocol.VerificationModeTBCI &&
		shouldRunAgentVerifier(payload)
}

func shouldPrepareTBCIVerifier(profile, verificationMode string) bool {
	return normalizeOrchestrationProfile(profile) == "formal-proof" &&
		protocol.NormalizeVerificationMode(verificationMode) == protocol.VerificationModeTBCI
}

func tbciVerifierVerdict(result tbciVerificationResult) protocol.VerifierVerdict {
	reason := boundedTBCIReason(result.Reason)
	if reason == "" {
		reason = "TBCI did not report Trusted"
	}
	status := verifierVerdictContinue
	evidence := []string{"TBCI did not authorize completion"}
	if result.Trusted {
		status = verifierVerdictPass
		evidence = []string{"TBCI independently verified the frozen contract against initial and final workspace snapshots"}
	}
	return protocol.VerifierVerdict{
		Status:   status,
		Reason:   reason,
		Evidence: evidence,
		Checkers: []protocol.VerifierCheck{{Name: "tbci", Status: status, Reason: reason}},
	}
}

func (m *OrchestrationManager) prepareTBCIVerifier(ctx context.Context, payload protocol.OrchestrationStartPayload, originalPrompt string) (*tbciVerificationSession, error) {
	workspace, err := canonicalTBCIWorkspace(m.cwd(payload))
	if err != nil {
		return nil, err
	}
	taskPrompt := strings.TrimSpace(originalPrompt)
	if taskPrompt == "" {
		taskPrompt = exactOrchestrationTaskPrompt(payload)
	}
	taskDigest := tbciTaskDigest(payload.RunID, payload.PromptSeq, taskPrompt)
	runtime := tbciSessionRuntime(payload, taskDigest)
	session := &tbciVerificationSession{
		runtime: runtime, initial: filepath.Join(runtime, "initial"), contract: filepath.Join(runtime, "contract.json"),
		verifierPriv: filepath.Join(runtime, "verifier-private"), workspace: workspace, taskDigest: taskDigest,
	}
	if loaded, ok, err := loadTBCISession(session); err != nil {
		return nil, err
	} else if ok {
		if !tbciWorkspaceContains(workspace, loaded.Workspace) || loaded.TaskDigest != taskDigest {
			return nil, errors.New("TBCI session identity changed for the same run and prompt sequence")
		}
		session.workspace = loaded.Workspace
		session.adapter = loaded.Adapter
		session.templateSource = loaded.TemplateSource
		session.available = true
		return session, nil
	}
	if err := os.MkdirAll(filepath.Dir(runtime), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(runtime); err == nil {
		if err := os.RemoveAll(runtime); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(runtime), ".prepare-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	staged := &tbciVerificationSession{
		runtime: staging, initial: filepath.Join(staging, "initial"), contract: filepath.Join(staging, "contract.json"),
		verifierPriv: filepath.Join(staging, "verifier-private"), workspace: workspace, taskDigest: taskDigest,
	}
	paths, err := findTBCITemplates(workspace)
	if err != nil {
		return nil, err
	}
	if len(paths) > 1 {
		return nil, errors.New("TBCI contract selection is ambiguous: multiple explicit templates were found")
	}
	templatePath := ""
	if len(paths) == 1 {
		templatePath = paths[0]
		staged.templateSource = "explicit"
	} else {
		templatePath = filepath.Join(staging, "generated-template.json")
		_, proofWorkspace, err := generateTBCITemplateAndWorkspace(workspace, taskPrompt, templatePath)
		if err != nil {
			return nil, err
		}
		workspace = proofWorkspace
		staged.workspace = workspace
		session.workspace = workspace
		staged.templateSource = "generated"
	}
	template, err := validateTBCITemplate(templatePath)
	if err != nil {
		return nil, fmt.Errorf("TBCI contract is invalid: %w", err)
	}
	staged.adapter = template.Goal.Prover
	if err := copyWorkspaceSnapshot(workspace, staged.initial); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(staging, "task-prompt.txt"), []byte(taskPrompt), 0o600); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staged.verifierPriv, 0o700); err != nil {
		return nil, err
	}
	if _, err := m.runTBCIPhase(ctx, []string{"contract", "freeze", "--adapter", staged.adapter, "--input", templatePath, "--workspace", staged.initial, "--output", staged.contract}); err != nil {
		return nil, errors.New("TBCI contract freeze failed: " + boundedTBCIReason(err.Error()))
	}
	metadata := tbciSessionMetadata{SchemaVersion: "proofbridge-tbci-session-v1", Workspace: workspace, TaskDigest: taskDigest, Adapter: staged.adapter, TemplateSource: staged.templateSource}
	if err := writeTBCISessionMetadata(filepath.Join(staging, "session.json"), metadata); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, runtime); err != nil {
		if loaded, ok, loadErr := loadTBCISession(session); loadErr == nil && ok && tbciWorkspaceContains(canonicalParentWorkspace(m.cwd(payload)), loaded.Workspace) && loaded.TaskDigest == taskDigest {
			session.workspace, session.adapter, session.templateSource, session.available = loaded.Workspace, loaded.Adapter, loaded.TemplateSource, true
			return session, nil
		}
		return nil, err
	}
	session.adapter, session.templateSource, session.available = staged.adapter, staged.templateSource, true
	return session, nil
}

func canonicalParentWorkspace(path string) string {
	workspace, _ := canonicalTBCIWorkspace(path)
	return workspace
}

func tbciWorkspaceContains(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func canonicalTBCIWorkspace(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("TBCI workspace is not a directory")
	}
	return filepath.Clean(real), nil
}

func tbciTaskDigest(runID string, promptSeq int64, prompt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(runID) + "\n" + fmt.Sprint(promptSeq) + "\n" + strings.TrimSpace(prompt)))
	return hex.EncodeToString(sum[:])
}

func tbciSessionRuntime(payload protocol.OrchestrationStartPayload, taskDigest string) string {
	run := safeOrchestrationFileName.ReplaceAllString(strings.TrimSpace(payload.RunID), "-")
	if run == "" {
		run = "run"
	}
	identity := fmt.Sprintf("prompt-%d", payload.PromptSeq)
	if payload.PromptSeq <= 0 {
		identity = "legacy-" + taskDigest[:16]
	}
	return filepath.Join(strictWorkspaceRuntimeBase(), "tbci", run, identity)
}

func loadTBCISession(session *tbciVerificationSession) (tbciSessionMetadata, bool, error) {
	var metadata tbciSessionMetadata
	raw, err := os.ReadFile(filepath.Join(session.runtime, "session.json"))
	if errors.Is(err, os.ErrNotExist) {
		return metadata, false, nil
	}
	if err != nil {
		return metadata, false, err
	}
	if err := json.Unmarshal(raw, &metadata); err != nil || metadata.SchemaVersion != "proofbridge-tbci-session-v1" || metadata.Adapter == "" {
		return metadata, false, errors.New("TBCI session metadata is invalid")
	}
	for _, path := range []string{session.initial, session.contract, session.verifierPriv} {
		if _, err := os.Stat(path); err != nil {
			return metadata, false, fmt.Errorf("TBCI session is incomplete: %w", err)
		}
	}
	return metadata, true, nil
}

func writeTBCISessionMetadata(path string, metadata tbciSessionMetadata) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

var (
	tbciRocqDeclaration     = regexp.MustCompile(`(?m)^\s*(?:Theorem|Lemma|Fact|Corollary|Proposition|Remark|Example)\s+([[:alpha:]_][[:alnum:]_']*)\s*(?:\([^\n]*\)\s*)*:`)
	tbciLeanDeclaration     = regexp.MustCompile(`(?m)^\s*(?:theorem|lemma)\s+([[:alpha:]_][[:alnum:]_'.]*)\s*(?:\([^\n]*\)\s*)*:`)
	tbciIsabelleDeclaration = regexp.MustCompile(`(?m)^\s*(?:theorem|lemma|corollary|proposition)\s+([[:alpha:]_][[:alnum:]_'.]*)\s*:`)
)

func generateTBCITemplate(workspace, prompt, output string) (tbciTemplate, error) {
	template, _, err := generateTBCITemplateAndWorkspace(workspace, prompt, output)
	return template, err
}

func generateTBCITemplateAndWorkspace(workspace, prompt, output string) (tbciTemplate, string, error) {
	candidates, err := discoverTBCITargets(workspace)
	if err != nil {
		return tbciTemplate{}, "", err
	}
	target, err := selectTBCITarget(candidates, prompt)
	if err != nil {
		return tbciTemplate{}, "", err
	}
	proofWorkspace := discoverTBCIProofWorkspace(workspace, target)
	relTarget, err := filepath.Rel(proofWorkspace, filepath.Join(workspace, filepath.FromSlash(target.Path)))
	if err != nil {
		return tbciTemplate{}, "", err
	}
	target.Path = filepath.ToSlash(relTarget)
	if target.Prover == "lean4" {
		target.Module = strings.ReplaceAll(strings.TrimSuffix(target.Path, filepath.Ext(target.Path)), "/", ".")
	}
	var template tbciTemplate
	template.SchemaVersion = "tbci-contract-v1"
	template.Goal.Prover = target.Prover
	template.Goal.Module = target.Module
	template.Goal.Name = target.Name
	template.AuthorizedPaths = []string{target.Path}
	template.Trust = map[string]any{"imports": []string{}, "allow_unsafe": false, "allow_native_plugins": false}
	template.Environment = map[string]any{}
	template.Policy.RequiredEvidence = []string{"target", "trust", "environment", "kernel"}
	template.Policy.RequireVerifierObservation = true
	switch target.Prover {
	case "rocq":
		template.Verification.Predicate = "rocq-kernel-check-and-assumption-audit"
		template.Verification.Checker = "coqc+coqtop"
	case "lean4":
		template.Verification.Predicate = "lean-kernel-check-and-axiom-audit"
		template.Verification.Checker = "lean"
	case "isabelle":
		template.Verification.Predicate = "isabelle-build-and-oracle-audit"
		template.Verification.Checker = "isabelle-build"
	}
	raw, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return tbciTemplate{}, "", err
	}
	if err := os.WriteFile(output, append(raw, '\n'), 0o600); err != nil {
		return tbciTemplate{}, "", err
	}
	return template, proofWorkspace, nil
}

func discoverTBCIProofWorkspace(selected string, target tbciTargetCandidate) string {
	sourceDir := filepath.Dir(filepath.Join(selected, filepath.FromSlash(target.Path)))
	markers := []string{}
	switch target.Prover {
	case "rocq":
		markers = []string{"_CoqProject", "dune-project"}
	case "lean4":
		markers = []string{"lakefile.lean", "lakefile.toml", "lean-toolchain"}
	case "isabelle":
		markers = []string{"ROOT"}
	}
	for current := sourceDir; tbciWorkspaceContains(selected, current); current = filepath.Dir(current) {
		for _, marker := range markers {
			if info, err := os.Stat(filepath.Join(current, marker)); err == nil && !info.IsDir() {
				return current
			}
		}
		if current == selected || filepath.Dir(current) == current {
			break
		}
	}
	return sourceDir
}

func discoverTBCITargets(workspace string) ([]tbciTargetCandidate, error) {
	var candidates []tbciTargetCandidate
	err := filepath.WalkDir(workspace, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".codex-bridge", ".tbci", "node_modules", ".lake", "build", "dist", "target", "_build":
				if path != workspace {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		var prover, module string
		var pattern *regexp.Regexp
		switch ext {
		case ".v":
			prover, pattern = "rocq", tbciRocqDeclaration
			module = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		case ".lean":
			prover, pattern = "lean4", tbciLeanDeclaration
			rel, err := filepath.Rel(workspace, strings.TrimSuffix(path, filepath.Ext(path)))
			if err != nil {
				return err
			}
			module = strings.ReplaceAll(filepath.ToSlash(rel), "/", ".")
		case ".thy":
			prover, pattern = "isabelle", tbciIsabelleDeclaration
			module = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		default:
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		for _, match := range pattern.FindAllSubmatch(raw, -1) {
			candidates = append(candidates, tbciTargetCandidate{Prover: prover, Module: module, Name: string(match[1]), Path: filepath.ToSlash(rel)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Path != candidates[j].Path {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Name < candidates[j].Name
	})
	return candidates, nil
}

func selectTBCITarget(candidates []tbciTargetCandidate, prompt string) (tbciTargetCandidate, error) {
	if len(candidates) == 0 {
		return tbciTargetCandidate{}, errors.New("TBCI could not generate a contract: no supported theorem or lemma declaration exists in the prepared repository")
	}
	lower := strings.ToLower(prompt)
	requestedProver := ""
	switch {
	case strings.Contains(lower, "isabelle"):
		requestedProver = "isabelle"
	case strings.Contains(lower, "lean"):
		requestedProver = "lean4"
	case strings.Contains(lower, "rocq") || strings.Contains(lower, "coq"):
		requestedProver = "rocq"
	}
	filtered := candidates[:0]
	for _, candidate := range candidates {
		if requestedProver == "" || candidate.Prover == requestedProver {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		return tbciTargetCandidate{}, fmt.Errorf("TBCI could not generate a contract: the requested %s target was not found", requestedProver)
	}
	mentioned := make([]tbciTargetCandidate, 0, len(filtered))
	for _, candidate := range filtered {
		namePattern := regexp.MustCompile(`(?i)(^|[^[:alnum:]_'])` + regexp.QuoteMeta(candidate.Name) + `([^[:alnum:]_']|$)`)
		if namePattern.MatchString(prompt) {
			mentioned = append(mentioned, candidate)
		}
	}
	if len(mentioned) == 1 {
		return mentioned[0], nil
	}
	if len(mentioned) > 1 {
		byFile := filterTBCICandidatesByMentionedPath(mentioned, lower)
		if len(byFile) == 1 {
			return byFile[0], nil
		}
		return tbciTargetCandidate{}, fmt.Errorf("TBCI could not generate a contract: %d target declarations are named by the user task", len(mentioned))
	}
	byFile := filterTBCICandidatesByMentionedPath(filtered, lower)
	if len(byFile) == 1 {
		return byFile[0], nil
	}
	if len(filtered) == 1 {
		return filtered[0], nil
	}
	return tbciTargetCandidate{}, fmt.Errorf("TBCI could not generate a contract: target is ambiguous across %d declarations; name one theorem or provide an explicit template", len(filtered))
}

func filterTBCICandidatesByMentionedPath(candidates []tbciTargetCandidate, lowerPrompt string) []tbciTargetCandidate {
	var out []tbciTargetCandidate
	for _, candidate := range candidates {
		path := strings.ToLower(candidate.Path)
		if strings.Contains(lowerPrompt, path) || strings.Contains(lowerPrompt, strings.ToLower(filepath.Base(path))) {
			out = append(out, candidate)
		}
	}
	return out
}

func findTBCITemplates(workspace string) ([]string, error) {
	candidates := []string{
		filepath.Join(workspace, ".tbci", "task-template.json"),
		filepath.Join(workspace, "tbci-task-template.json"),
		filepath.Join(workspace, "task-template.json"),
	}
	var found []string
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			found = append(found, path)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return found, nil
}

func validateTBCITemplate(path string) (tbciTemplate, error) {
	var t tbciTemplate
	b, err := os.ReadFile(path)
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return t, errors.New("contract template is invalid JSON")
	}
	if t.SchemaVersion != "tbci-contract-v1" {
		return t, errors.New("contract template schema is unsupported")
	}
	p := strings.ToLower(strings.TrimSpace(t.Goal.Prover))
	if p == "coq" {
		p = "rocq"
	}
	if p != "rocq" && p != "lean" && p != "lean4" && p != "isabelle" {
		return t, errors.New("contract template prover is unsupported")
	}
	t.Goal.Prover = p
	if strings.TrimSpace(t.Goal.Module) == "" || strings.TrimSpace(t.Goal.Name) == "" {
		return t, errors.New("contract template goal is incomplete")
	}
	if len(t.AuthorizedPaths) == 0 {
		return t, errors.New("contract template has no authorized paths")
	}
	if len(t.Policy.RequiredEvidence) == 0 || strings.TrimSpace(t.Verification.Predicate) == "" || strings.TrimSpace(t.Verification.Checker) == "" {
		return t, errors.New("contract template policy or verification is incomplete")
	}
	return t, nil
}

func (m *OrchestrationManager) evaluateTBCIVerifier(ctx context.Context, payload protocol.OrchestrationStartPayload, session *tbciVerificationSession) tbciVerificationResult {
	result := tbciVerificationResult{Attempted: true}
	if session == nil || !session.available {
		result.Reason = "TBCI is unavailable"
		return result
	}
	selected, err := canonicalTBCIWorkspace(m.cwd(payload))
	workspace, workspaceErr := canonicalTBCIWorkspace(session.workspace)
	if err != nil || workspaceErr != nil || workspace != session.workspace || !tbciWorkspaceContains(selected, workspace) {
		result.Reason = "TBCI final workspace root differs from the frozen initial repository"
		result.Error = errors.Join(err, workspaceErr)
		return result
	}
	final := filepath.Join(session.runtime, "final")
	if err := os.RemoveAll(final); err != nil {
		result.Reason = "TBCI could not reset the private final workspace snapshot"
		result.Error = err
		return result
	}
	if err := copyWorkspaceSnapshot(workspace, final); err != nil {
		result.Reason = "TBCI could not create the final workspace snapshot"
		result.Error = err
		return result
	}
	attestation := filepath.Join(session.runtime, "attestation.json")
	_ = os.Remove(attestation)
	if err := os.RemoveAll(session.verifierPriv); err != nil {
		result.Reason = "TBCI could not reset verifier-private state"
		result.Error = err
		return result
	}
	if err := os.MkdirAll(session.verifierPriv, 0o700); err != nil {
		result.Reason = "TBCI could not create verifier-private state"
		result.Error = err
		return result
	}
	if _, err := m.runTBCIPhase(ctx, []string{"attest", "--adapter", session.adapter, "--contract", session.contract, "--before", session.initial, "--after", final, "--output", attestation}); err != nil {
		result.Reason = "TBCI attest failed: " + boundedTBCIReason(err.Error())
		result.Error = err
		return result
	}
	output, err := m.runTBCIPhase(ctx, []string{"verify", "--adapter", session.adapter, "--contract", session.contract, "--attestation", attestation, "--before", session.initial, "--after", final, "--handoff-private-parent", session.verifierPriv})
	verdict, reason := parseTBCIVerdict(output)
	result.Verdict, result.Reason, result.Trusted = verdict, reason, strings.EqualFold(verdict, "trusted")
	if err != nil {
		result.Error = err
		if result.Reason == "" {
			result.Reason = "TBCI verify failed: " + boundedTBCIReason(err.Error())
		}
	}
	if result.Reason == "" {
		if result.Trusted {
			result.Reason = "TBCI reported Trusted"
		} else {
			result.Reason = "TBCI did not report Trusted"
		}
	}
	return result
}

func (m *OrchestrationManager) runTBCIPhase(ctx context.Context, args []string) ([]byte, error) {
	select {
	case tbciVerificationGate <- struct{}{}:
		defer func() { <-tbciVerificationGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	timeout := 2 * time.Minute
	if m.cfg != nil {
		timeout = m.cfg.Bridge.TBCITimeout.Duration
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	path := ""
	if runtime := m.tbciRuntime(); runtime != nil {
		path = strings.TrimSpace(runtime.path)
	}
	if path == "" && m.cfg != nil {
		path = strings.TrimSpace(m.cfg.Bridge.TBCIPath)
	}
	if path == "" {
		return nil, errors.New("TBCI runtime path is unavailable")
	}
	return runTBCICommand(callCtx, path, args)
}

func runTBCICommand(ctx context.Context, path string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "TBCI_NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limitedBuffer{Buffer: &stdout}, &limitedBuffer{Buffer: &stderr}
	err := cmd.Run()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			message = "TBCI verification timed out"
		}
		if message == "" {
			message = err.Error()
		}
		return stdout.Bytes(), errors.New(message)
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct{ *bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.Len() >= 1<<20 {
		return written, nil
	}
	remaining := (1 << 20) - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, err := b.Buffer.Write(p)
	return written, err
}

func boundedTBCIReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		return reason[:500]
	}
	return reason
}

func parseTBCIVerdict(raw []byte) (string, string) {
	var value any
	if json.Unmarshal(bytes.TrimSpace(raw), &value) != nil {
		return "", "TBCI returned invalid JSON"
	}
	return findStringField(value, "verdict"), findStringField(value, "reason")
}

func findStringField(value any, key string) string {
	switch item := value.(type) {
	case map[string]any:
		for name, child := range item {
			if strings.EqualFold(name, key) {
				if text, ok := child.(string); ok {
					return text
				}
			}
			if found := findStringField(child, key); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range item {
			if found := findStringField(child, key); found != "" {
				return found
			}
		}
	}
	return ""
}

func copyWorkspaceSnapshot(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("workspace is not a directory")
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	var total int64
	var files int
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".tbci" || entry.Name() == ".codex-bridge" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(destination, rel), 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		files++
		if files > tbciMaxSnapshotFiles {
			return errors.New("workspace snapshot contains too many files")
		}
		fi, err := entry.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		if total > tbciMaxSnapshotBytes {
			return errors.New("workspace snapshot is too large")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		outPath := filepath.Join(destination, rel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
			_ = in.Close()
			return err
		}
		out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		inCloseErr := in.Close()
		outCloseErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inCloseErr != nil {
			return inCloseErr
		}
		return outCloseErr
	})
	return err
}
